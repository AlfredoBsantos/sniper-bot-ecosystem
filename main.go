package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/joho/godotenv"
	"github.com/segmentio/kafka-go"
)

// Estrutura de Dados Final e Completa
type EnrichedTransactionData struct {
	Hash          string       `json:"hash"`
	To            string       `json:"to"`
	From          string       `json:"from"`
	Nonce         uint64       `json:"nonce"`
	GasPrice      string       `json:"gasPrice"`
	GasLimit      uint64       `json:"gasLimit"`
	GasUsed       uint64       `json:"gasUsed"`
	BaseFeePerGas string       `json:"baseFeePerGas"` // O CAMPO FINAL
	Value         string       `json:"value"`
	Timestamp     int64        `json:"timestamp"`
	InputData     string       `json:"inputData"`
	Logs          []*types.Log `json:"logs"`
}

type Config struct {
	TargetContracts map[common.Address]string
}

func processBlock(block *types.Block, client *ethclient.Client, kafkaWriter *kafka.Writer, cfg *Config) {
	chainID, err := client.NetworkID(context.Background())
	if err != nil {
		log.Printf("Falha ao obter ChainID ao processar bloco: %v", err)
		return
	}

	// Pega a taxa base do bloco, a informação que faltava
	baseFee := block.BaseFee()

	for _, tx := range block.Transactions() {
		if tx.To() == nil {
			continue
		}

		if contractName, ok := cfg.TargetContracts[*tx.To()]; ok {
			receipt, err := client.TransactionReceipt(context.Background(), tx.Hash())
			if err != nil {
				log.Printf("Falha ao obter recibo para tx %s: %v", tx.Hash().Hex(), err)
				continue
			}

			log.Printf(">>> Alvo Encontrado (%s)! Coletando dados completos: %s", contractName, tx.Hash().Hex())

			var fromAddress string
			if from, err := types.Sender(types.NewEIP155Signer(chainID), tx); err == nil {
				fromAddress = from.Hex()
			}

			data := EnrichedTransactionData{
				Hash:          tx.Hash().Hex(),
				To:            tx.To().Hex(),
				From:          fromAddress,
				Nonce:         tx.Nonce(),
				GasPrice:      tx.GasPrice().String(),
				GasLimit:      tx.Gas(),
				GasUsed:       receipt.GasUsed,
				BaseFeePerGas: baseFee.String(), // Adicionamos a nova informação
				Value:         tx.Value().String(),
				Timestamp:     int64(block.Time()),
				InputData:     "0x" + common.Bytes2Hex(tx.Data()),
				Logs:          receipt.Logs,
			}

			jsonData, err := json.Marshal(data)
			if err != nil {
				continue
			}

			err = kafkaWriter.WriteMessages(context.Background(), kafka.Message{
				Key:   []byte(tx.Hash().Hex()),
				Value: jsonData,
			},
			)
			if err != nil {
				log.Printf("!!! Falha ao enviar mensagem para o Kafka: %v", err)
			}
		}
	}
}

func listenToNewBlocks(client *ethclient.Client, kafkaWriter *kafka.Writer, cfg *Config) {
	headers := make(chan *types.Header)
	sub, err := client.SubscribeNewHead(context.Background(), headers)
	if err != nil {
		log.Printf("Falha ao se inscrever para novos blocos: %v", err)
		return
	}
	defer sub.Unsubscribe()

	log.Println("Coletor de Dados v3 (Completo) iniciado. Monitorando novos blocos na Mainnet...")

	for {
		select {
		case err := <-sub.Err():
			log.Printf("Erro na subscrição (conexão perdida): %v", err)
			return
		case header := <-headers:
			block, err := client.BlockByHash(context.Background(), header.Hash())
			if err != nil {
				log.Printf("Erro ao buscar bloco %s: %v", header.Hash().Hex(), err)
				continue
			}
			go processBlock(block, client, kafkaWriter, cfg)
		}
	}
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Erro ao carregar o arquivo .env")
	}
	wssURL := os.Getenv("ALCHEMY_WSS_URL")
	if wssURL == "" {
		log.Fatal("ERRO: ALCHEMY_WSS_URL deve estar definido no .env")
	}

	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP("localhost:9092"),
		Topic:    "mempool-transactions",
		Balancer: &kafka.LeastBytes{},
	}
	defer kafkaWriter.Close()

	cfg := &Config{
		TargetContracts: map[common.Address]string{
			common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"): "Uniswap V2 Router",
			common.HexToAddress("0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45"): "Uniswap V3 Router",
			common.HexToAddress("0xd9e1cE17f2641f24aE83637ab66a2cca9C378B9F"): "Sushiswap Router",
			common.HexToAddress("0x111111125421cA6dc452d289314280a0f8842A65"): "1inch Aggregation Router",
		},
	}

	for {
		log.Println("Tentando conectar ao nó Ethereum...")
		rpcClient, err := rpc.Dial(wssURL)
		if err != nil {
			log.Printf("Falha ao conectar. Tentando novamente em 15 segundos... Erro: %v", err)
			time.Sleep(15 * time.Second)
			continue
		}

		client := ethclient.NewClient(rpcClient)
		listenToNewBlocks(client, kafkaWriter, cfg)
		rpcClient.Close()
		log.Println("Conexão perdida. Tentando reconectar em 15 segundos...")
		time.Sleep(15 * time.Second)
	}
}
