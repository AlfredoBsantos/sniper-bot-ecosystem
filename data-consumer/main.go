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

	_ "github.com/lib/pq"
	"github.com/segmentio/kafka-go"
)

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
}

func main() {
	connStr := "postgres://admin:supersecret@localhost:5433/mempool_data?sslmode=disable"
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("DB Connect: %v", err)
	}
	defer db.Close()

	kafkaReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{"localhost:9092"},
		Topic:   "mempool-transactions",
		GroupID: "storage-group-final",
	})
	defer kafkaReader.Close()

	log.Println("Consumidor Final iniciado. Aguardando dados brutos...")
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigchan:
			log.Println("Encerrando...")
			return
		default:
			m, err := kafkaReader.ReadMessage(context.Background())
			if err != nil {
				continue
			}

			var data TransactionData
			if err := json.Unmarshal(m.Value, &data); err != nil {
				log.Printf("Erro JSON Unmarshal: %v", err)
				continue
			}

			// A tabela precisa apenas das colunas que estamos salvando
			sqlStatement := `INSERT INTO transactions (hash, to_address, from_address, "inputData", event_timestamp) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (hash) DO NOTHING;`

			_, err = db.Exec(sqlStatement, data.Hash, data.To, data.From, data.InputData, time.Unix(data.Timestamp, 0))
			if err != nil {
				log.Printf("Erro DB Insert: %v", err)
			} else {
				log.Printf("[DADO BRUTO SALVO] Hash: %s", data.Hash)
			}
		}
	}
}
