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

// Estrutura de dados que enviamos para o Kafka
type TransactionData struct {
	Hash      string `json:"hash"`
	To        string `json:"to"`
	From      string `json:"from"`
	Nonce     uint64 `json:"nonce"`
	GasPrice  string `json:"gasPrice"`
	GasLimit  uint64 `json:"gasLimit"`
	Value     string `json:"value"`
	Timestamp int64  `json:"timestamp"`
	InputData string `json:"inputData"`
	// BaseFeePerGas será adicionado na análise, não na coleta
}

func main() {
	if err := godotenv.Load("../.env"); err != nil {
		log.Fatalf("Erro ao carregar o arquivo .env: %v", err)
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

	targetContracts := map[common.Address]string{
		common.HexToAddress("0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D"): "Uniswap V2 Router",
		common.HexToAddress("0x68b3465833fb72A70ecDF485E0e4C7bD8665Fc45"): "Uniswap V3 Router",
		common.HexToAddress("0xd9e1cE17f2641f24aE83637ab66a2cca9C378B9F"): "Sushiswap Router",
		common.HexToAddress("0x111111125421cA6dc452d289314280a0f8842A65"): "1inch Aggregation Router",
	}

	for {
		log.Println("Tentando conectar ao nó Ethereum...")
		rpcClient, err := rpc.Dial(wssURL)
		if err != nil {
			log.Printf("Falha ao conectar. Tentando novamente... Erro: %v", err)
			time.Sleep(15 * time.Second)
			continue
		}

		client := ethclient.NewClient(rpcClient)
		txs := make(chan common.Hash)
		sub, err := rpcClient.EthSubscribe(context.Background(), txs, "newPendingTransactions")
		if err != nil {
			log.Printf("Falha ao se inscrever no Mempool: %v", err)
			rpcClient.Close()
			continue
		}

		log.Println("Coletor de Dados vFinal iniciado. Monitorando o Mempool...")

	Loop:
		for {
			select {
			case err := <-sub.Err():
				log.Printf("Erro na subscrição (conexão perdida): %v", err)
				break Loop
			case txHash := <-txs:
				go func(hash common.Hash) {
					tx, isPending, _ := client.TransactionByHash(context.Background(), hash)
					if !isPending || tx.To() == nil {
						return
					}
					if _, ok := targetContracts[*tx.To()]; ok {
						chainID, _ := client.NetworkID(context.Background())
						from, _ := types.Sender(types.NewEIP155Signer(chainID), tx)

						data := TransactionData{
							Hash:      hash.Hex(),
							To:        tx.To().Hex(),
							From:      from.Hex(),
							Nonce:     tx.Nonce(),
							GasPrice:  tx.GasPrice().String(),
							GasLimit:  tx.Gas(),
							Value:     tx.Value().String(),
							Timestamp: time.Now().Unix(),
							InputData: "0x" + common.Bytes2Hex(tx.Data()),
						}
						jsonData, _ := json.Marshal(data)

						err = kafkaWriter.WriteMessages(context.Background(), kafka.Message{Value: jsonData})
						if err != nil {
							log.Printf("!!! Falha ao enviar para o Kafka: %v", err)
						} else {
							log.Printf(">>> Alvo Detectado! Dados brutos enviados para o Kafka: %s", hash.Hex())
						}
					}
				}(txHash)
			}
		}
		sub.Unsubscribe()
		rpcClient.Close()
		log.Println("Conexão perdida. Tentando reconectar...")
		time.Sleep(5 * time.Second)
	}
}
