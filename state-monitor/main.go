package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

// ABI simplificado do contrato de Par da Uniswap V2, apenas com a função getReserves
const pairABI = `[{"constant":true,"inputs":[],"name":"getReserves","outputs":[{"internalType":"uint112","name":"_reserve0","type":"uint112"},{"internalType":"uint112","name":"_reserve1","type":"uint112"},{"internalType":"uint32","name":"_blockTimestampLast","type":"uint32"}],"payable":false,"stateMutability":"view","type":"function"}]`

func main() {
	// Carrega o .env
	if err := godotenv.Load("../.env"); err != nil {
		log.Fatal("Erro ao carregar o arquivo .env da pasta raiz")
	}
	wssURL := os.Getenv("ALCHEMY_WSS_URL")
	if wssURL == "" {
		log.Fatal("ERRO: ALCHEMY_WSS_URL deve estar definido no .env")
	}

	// Conecta ao nó
	client, err := ethclient.Dial(wssURL)
	if err != nil {
		log.Fatalf("Falha ao conectar: %v", err)
	}

	// Prepara o ABI para fazer as chamadas
	pairContractABI, err := abi.JSON(strings.NewReader(pairABI))
	if err != nil {
		log.Fatal(err)
	}

	// --- Nossos Alvos de Monitoramento de Liquidez (Pools da Uniswap V2 na Mainnet) ---
	poolsToMonitor := map[string]string{
		"WETH/USDC": "0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc",
		"WETH/DAI":  "0xA478c2975Ab1Ea89e8196811F51A7B7Ade33EB11",
		"WETH/USDT": "0x0d4a11d5EEaaC28EC3F61d100daF4d40471f1852",
	}

	// Inscreve-se para novos blocos
	headers := make(chan *types.Header)
	sub, err := client.SubscribeNewHead(context.Background(), headers)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- Cartógrafo Iniciado: Monitorando o estado dos pools de liquidez na Mainnet ---")

	for {
		select {
		case err := <-sub.Err():
			log.Fatal(err)
		case header := <-headers:
			fmt.Printf("\n--- Novo Bloco Detectado: #%s ---\n", header.Number.String())

			// Para cada bloco novo, verificamos as reservas de cada pool
			for name, address := range poolsToMonitor {
				pairAddress := common.HexToAddress(address)

				// Monta a chamada para a função getReserves
				packedData, _ := pairContractABI.Pack("getReserves")
				callMsg := ethereum.CallMsg{To: &pairAddress, Data: packedData}

				// Executa a chamada no estado do bloco que acabamos de receber
				result, err := client.CallContract(context.Background(), callMsg, header.Number)
				if err != nil {
					log.Printf("Falha ao buscar reservas para %s: %v", name, err)
					continue
				}

				// Decodifica o resultado
				type Reserves struct {
					Reserve0, Reserve1 *big.Int
					BlockTimestampLast uint32
				}
				var reserves Reserves
				if err := pairContractABI.UnpackIntoInterface(&reserves, "getReserves", result); err != nil {
					log.Printf("Falha ao decodificar reservas para %s: %v", name, err)
					continue
				}

				fmt.Printf("Pool: %s | Reserva 0: %s | Reserva 1: %s\n", name, reserves.Reserve0.String(), reserves.Reserve1.String())
			}
		}
	}
}
