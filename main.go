package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/joho/godotenv"
)

// ABIs
const uniswapV2RouterABI = `[{"inputs":[{"internalType":"uint256","name":"amountIn","type":"uint256"},{"internalType":"uint256","name":"amountOutMin","type":"uint256"},{"internalType":"address[]","name":"path","type":"address[]"},{"internalType":"address","name":"to","type":"address"},{"internalType":"uint256","name":"deadline","type":"uint256"}],"name":"swapExactTokensForTokens","outputs":[{"internalType":"uint256[]","name":"amounts","type":"uint256[]"}],"stateMutability":"nonpayable","type":"function"}]`
const factoryABI = `[{"constant":true,"inputs":[{"internalType":"address","name":"tokenA","type":"address"},{"internalType":"address","name":"tokenB","type":"address"}],"name":"getPair","outputs":[{"internalType":"address","name":"pair","type":"address"}],"payable":false,"stateMutability":"view","type":"function"}]`
const pairABI = `[{"constant":true,"inputs":[],"name":"getReserves","outputs":[{"internalType":"uint112","name":"_reserve0","type":"uint112"},{"internalType":"uint112","name":"_reserve1","type":"uint112"},{"internalType":"uint32","name":"_blockTimestampLast","type":"uint32"}],"payable":false,"stateMutability":"view","type":"function"},{"constant":true,"inputs":[],"name":"token0","outputs":[{"internalType":"address","name":"","type":"address"}],"payable":false,"stateMutability":"view","type":"function"}]`
const flashLoanExecutorABI = `[{"inputs":[{"internalType":"address","name":"_poolAddress","type":"address"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[{"internalType":"address","name":"_tokenIn","type":"address"},{"internalType":"address","name":"_tokenOut","type":"address"},{"internalType":"uint256","name":"_amount","type":"uint256"},{"internalType":"address","name":"_routerBuy","type":"address"},{"internalType":"address","name":"_routerSell","type":"address"}],"name":"startArbitrage","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"asset","type":"uint256","name":"amount","type":"uint256","name":"premium","type":"address","name":"initiator","type":"bytes","name":"params"}],"name":"executeOperation","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

// CORREÇÃO: Adicionamos o campo UniswapRouterAddress de volta à struct
type Config struct {
	Client                  *ethclient.Client
	PrivateKey              *ecdsa.PrivateKey
	FromAddress             common.Address
	FlashLoanContractAddr   common.Address
	TargetContracts         map[common.Address]string
	UniswapRouterAddress    common.Address
	UniswapFactoryAddress   common.Address
	SushiswapFactoryAddress common.Address
	RouterABI               abi.ABI
	ExecutorABI             abi.ABI
	FactoryABI              abi.ABI
	PairABI                 abi.ABI
}

func getAmountOut(cfg *Config, factoryAddress, tokenIn, tokenOut common.Address, amountIn *big.Int) (*big.Int, error) {
	packedData, err := cfg.FactoryABI.Pack("getPair", tokenIn, tokenOut)
	if err != nil {
		return nil, err
	}
	callMsg := ethereum.CallMsg{To: &factoryAddress, Data: packedData}
	result, err := cfg.Client.CallContract(context.Background(), callMsg, nil)
	if err != nil {
		return nil, fmt.Errorf("erro ao chamar getPair: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("chamada para getPair retornou vazio")
	}
	var pairAddress common.Address
	if err := cfg.FactoryABI.UnpackIntoInterface(&pairAddress, "getPair", result); err != nil {
		return nil, err
	}
	if pairAddress == (common.Address{}) {
		return nil, fmt.Errorf("par para %s/%s nao existe", tokenIn.Hex(), tokenOut.Hex())
	}

	packedData, err = cfg.PairABI.Pack("getReserves")
	if err != nil {
		return nil, err
	}
	callMsg = ethereum.CallMsg{To: &pairAddress, Data: packedData}
	result, err = cfg.Client.CallContract(context.Background(), callMsg, nil)
	if err != nil {
		return nil, fmt.Errorf("erro ao chamar getReserves: %w", err)
	}

	type Reserves struct{ Reserve0, Reserve1 *big.Int }
	var reserves Reserves
	if err := cfg.PairABI.UnpackIntoInterface(&reserves, "getReserves", result); err != nil {
		return nil, err
	}

	packedData, err = cfg.PairABI.Pack("token0")
	if err != nil {
		return nil, err
	}
	callMsg = ethereum.CallMsg{To: &pairAddress, Data: packedData}
	result, err = cfg.Client.CallContract(context.Background(), callMsg, nil)
	if err != nil {
		return nil, fmt.Errorf("erro ao chamar token0: %w", err)
	}
	var token0Address common.Address
	if err := cfg.PairABI.UnpackIntoInterface(&token0Address, "token0", result); err != nil {
		return nil, err
	}

	var reserveIn, reserveOut *big.Int
	if tokenIn == token0Address {
		reserveIn, reserveOut = reserves.Reserve0, reserves.Reserve1
	} else {
		reserveIn, reserveOut = reserves.Reserve1, reserves.Reserve0
	}

	if reserveIn.Cmp(big.NewInt(0)) == 0 || reserveOut.Cmp(big.NewInt(0)) == 0 {
		return nil, fmt.Errorf("liquidez insuficiente")
	}

	amountInWithFee := new(big.Int).Mul(amountIn, big.NewInt(997))
	numerator := new(big.Int).Mul(amountInWithFee, reserveOut)
	denominator := new(big.Int).Add(new(big.Int).Mul(reserveIn, big.NewInt(1000)), amountInWithFee)
	amountOut := new(big.Int).Div(numerator, denominator)
	return amountOut, nil
}

func executeArbitrage(cfg *Config, routerBuy, routerSell, tokenIn, tokenOut common.Address, amountIn *big.Int) {
	blockNumber, err := cfg.Client.BlockNumber(context.Background())
	if err != nil {
		log.Printf("Erro ao pegar o número do bloco para Flashbots: %v", err)
		return
	}

	log.Printf(">>> INICIANDO EXECUCAO DE ARBITRAGEM (Flashbots) PARA BLOCO %d <<<", blockNumber+1)

	nonce, err := cfg.Client.PendingNonceAt(context.Background(), cfg.FromAddress)
	if err != nil {
		log.Printf("Erro ao pegar nonce: %v", err)
		return
	}

	txData, err := cfg.ExecutorABI.Pack("startArbitrage", tokenIn, tokenOut, amountIn, routerBuy, routerSell)
	if err != nil {
		log.Printf("Erro ao empacotar tx data: %v", err)
		return
	}

	gasPrice, err := cfg.Client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Printf("Erro ao pegar gas price: %v", err)
		return
	}

	gasLimit := uint64(1000000)
	tx := types.NewTransaction(nonce, cfg.FlashLoanContractAddr, big.NewInt(0), gasLimit, gasPrice, txData)

	chainID, _ := cfg.Client.NetworkID(context.Background())
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), cfg.PrivateKey)
	if err != nil {
		log.Printf("Erro ao assinar a tx: %v", err)
		return
	}

	flashbotsRelayURL := "https://relay.flashbots.net"
	var rawTx bytes.Buffer
	if err := signedTx.EncodeRLP(&rawTx); err != nil {
		log.Printf("Erro ao codificar tx: %v", err)
		return
	}
	rawTxHex := "0x" + common.Bytes2Hex(rawTx.Bytes())

	nextBlockNumber := new(big.Int).SetUint64(blockNumber + 1)

	rpcRequest := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "eth_sendBundle", "params": []interface{}{map[string]interface{}{"txs": []string{rawTxHex}, "blockNumber": "0x" + nextBlockNumber.Text(16)}}}
	jsonData, _ := json.Marshal(rpcRequest)
	req, _ := http.NewRequest("POST", flashbotsRelayURL, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("!!! ERRO AO ENVIAR BUNDLE PARA FLASHBOTS: %v", err)
		return
	}
	defer resp.Body.Close()

	var rpcResponse map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&rpcResponse)
	if rpcResponse["error"] != nil {
		log.Printf("!!! FLASHBOTS RETORNOU UM ERRO: %v", rpcResponse["error"])
	} else {
		fmt.Printf(">>> Bundle enviado com sucesso para a Flashbots! Hash da transação principal: %s\n", signedTx.Hash().Hex())
	}
}

func processMempoolTx(tx *types.Transaction, cfg *Config) {
	if tx.To() == nil {
		return
	}

	targetRouterAddress := *tx.To()
	if _, ok := cfg.TargetContracts[targetRouterAddress]; !ok {
		return
	}

	method, err := cfg.RouterABI.MethodById(tx.Data()[:4])
	if err != nil {
		return
	}

	if method.Name == "swapExactTokensForTokens" {
		decodedData := make(map[string]interface{})
		if err := method.Inputs.UnpackIntoMap(decodedData, tx.Data()[4:]); err != nil {
			return
		}

		path, ok1 := decodedData["path"].([]common.Address)
		amountIn, ok2 := decodedData["amountIn"].(*big.Int)
		if !ok1 || !ok2 || len(path) < 2 {
			return
		}
		tokenIn, tokenOut := path[0], path[len(path)-1]

		log.Printf("[SWAP PENDENTE DETECTADO] Roteador: %s | Par: %s->%s", tx.To().Hex(), tokenIn.Hex(), tokenOut.Hex())

		sushiAmountOut, err1 := getAmountOut(cfg, cfg.SushiswapFactoryAddress, tokenIn, tokenOut, amountIn)
		uniAmountOut, err2 := getAmountOut(cfg, cfg.UniswapFactoryAddress, tokenIn, tokenOut, amountIn)

		if err1 != nil || err2 != nil {
			return
		}

		if sushiAmountOut.Cmp(uniAmountOut) > 0 {
			profit := new(big.Int).Sub(sushiAmountOut, uniAmountOut)
			fmt.Println("*************************************************")
			fmt.Println("!!! OPORTUNIDADE (SUSHI > UNI) DETECTADA !!!")
			fmt.Printf("Lucro Potencial: %s\n", profit.String())
			fmt.Println("*************************************************")

			executeArbitrage(cfg, cfg.SushiswapFactoryAddress, targetRouterAddress, tokenIn, tokenOut, amountIn)
		}
	}
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Erro ao carregar o arquivo .env")
	}

	wssURL := os.Getenv("ALCHEMY_WSS_URL")
	privateKeyHex := os.Getenv("BOT_PRIVATE_KEY")
	myFlashLoanContractAddress := os.Getenv("FLASHLOAN_CONTRACT_ADDRESS")

	if wssURL == "" || privateKeyHex == "" || myFlashLoanContractAddress == "" {
		log.Fatal("ERRO: Todas as variáveis de ambiente devem ser definidas.")
	}

	rpcClient, err := rpc.Dial(wssURL)
	if err != nil {
		log.Fatalf("Falha ao conectar ao nó: %v", err)
	}
	client := ethclient.NewClient(rpcClient)

	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		log.Fatal(err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, _ := publicKey.(*ecdsa.PublicKey)
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	routerABI, _ := abi.JSON(strings.NewReader(uniswapV2RouterABI))
	executorABI, _ := abi.JSON(strings.NewReader(flashLoanExecutorABI))
	factoryABI, _ := abi.JSON(strings.NewReader(factoryABI))
	pairABI, _ := abi.JSON(strings.NewReader(pairABI))

	targets := map[common.Address]string{
		common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"): "Uniswap V2 Router",
		common.HexToAddress("0xd9e1cE17f2641f24aE83637ab66a2cca9C378B9F"): "Sushiswap Router",
	}

	config := &Config{
		Client:                  client,
		PrivateKey:              privateKey,
		FromAddress:             fromAddress,
		FlashLoanContractAddr:   common.HexToAddress(myFlashLoanContractAddress),
		TargetContracts:         targets,
		UniswapRouterAddress:    common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"),
		UniswapFactoryAddress:   common.HexToAddress("0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f"),
		SushiswapFactoryAddress: common.HexToAddress("0xC0AEe478e3658e2610c5F7A4A2E1777cE9e4f2Ac"),
		RouterABI:               routerABI,
		ExecutorABI:             executorABI,
		FactoryABI:              factoryABI,
		PairABI:                 pairABI,
	}

	fmt.Println("Iniciando bot Híbrido Profissional (Mempool + Flashbots)...")

	txs := make(chan common.Hash)
	sub, err := rpcClient.EthSubscribe(context.Background(), txs, "newPendingTransactions")
	if err != nil {
		log.Fatalf("Falha ao se inscrever no Mempool: %v", err)
	}

	for {
		select {
		case err := <-sub.Err():
			log.Fatalf("Erro na subscrição: %v", err)
		case txHash := <-txs:
			go func(hash common.Hash) {
				tx, isPending, _ := client.TransactionByHash(context.Background(), hash)
				if isPending {
					processMempoolTx(tx, config)
				}
			}(txHash)
		}
	}
}
