package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	_ "github.com/lib/pq"
	"github.com/segmentio/kafka-go"
)

// CORREÇÃO 1: Adicionamos o novo campo `BaseFeePerGas` à nossa estrutura de dados.
type EnrichedTransactionData struct {
	Hash          string       `json:"hash"`
	To            string       `json:"to"`
	From          string       `json:"from"`
	Nonce         uint64       `json:"nonce"`
	GasPrice      string       `json:"gasPrice"`
	GasLimit      uint64       `json:"gasLimit"`
	GasUsed       uint64       `json:"gasUsed"`
	BaseFeePerGas string       `json:"baseFeePerGas"` // NOVO CAMPO
	Value         string       `json:"value"`
	Timestamp     int64        `json:"timestamp"`
	InputData     string       `json:"inputData"`
	Logs          []*types.Log `json:"logs"`
}

func main() {
	// ATENÇÃO: Verifique se a porta e a senha estão corretas
	connStr := "postgres://admin:supersecret@localhost:5433/mempool_data?sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Falha ao conectar ao banco de dados: %v", err)
	}
	defer db.Close()

	kafkaReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{"localhost:9092"},
		Topic:    "mempool-transactions",
		GroupID:  "storage-group-final-v2", // Novo group ID para evitar conflitos
		MinBytes: 10e3,
		MaxBytes: 10e6,
	})
	defer kafkaReader.Close()

	log.Println("Consumidor Enriquecido v2 iniciado. Salvando dados completos...")

	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigchan:
			log.Println("Recebido sinal de encerramento. Fechando...")
			return
		default:
			m, err := kafkaReader.ReadMessage(context.Background())
			if err != nil {
				log.Printf("Erro ao ler mensagem do Kafka: %v", err)
				continue
			}

			var data EnrichedTransactionData
			if err := json.Unmarshal(m.Value, &data); err != nil {
				log.Printf("Erro ao decodificar JSON: %v", err)
				continue
			}

			// CORREÇÃO 2: O comando SQL agora inclui a nova coluna 'base_fee_per_gas'.
			sqlStatement := `
				INSERT INTO transactions (hash, to_address, from_address, nonce, gas_price, gas_limit, value, event_timestamp, "inputData", base_fee_per_gas)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (hash) DO NOTHING;`

			_, err = db.Exec(sqlStatement, data.Hash, data.To, data.From, data.Nonce, data.GasPrice, data.GasLimit, data.Value, time.Unix(data.Timestamp, 0), data.InputData, data.BaseFeePerGas)
			if err != nil {
				log.Printf("Erro ao inserir no banco de dados: %v", err)
			} else {
				log.Printf("[DADO COMPLETO SALVO] Hash: %s", data.Hash)
			}
		}
	}
}
