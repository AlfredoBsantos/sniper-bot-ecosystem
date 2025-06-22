package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/segmentio/kafka-go"
)

type TransactionData struct {
	Hash      string `json:"hash"`
	To        string `json:"to"`
	From      string `json:"from"`
	InputData string `json:"inputData"`
	Timestamp int64  `json:"timestamp"`
}

func main() {
	// Procura o .env na pasta pai
	if err := godotenv.Load("../.env"); err != nil {
		log.Fatalf("Erro ao carregar .env da pasta raiz: %v", err)
	}

	// Usa variáveis de ambiente para a string de conexão para ser flexível
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, dbHost, dbPort, dbName)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Consumidor: Falha ao conectar ao DB: %v", err)
	}
	defer db.Close()

	kafkaBroker := os.Getenv("KAFKA_BROKER")
	kafkaReader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{kafkaBroker},
		Topic:   "mempool-transactions",
		GroupID: "storage-group-final",
	})
	defer kafkaReader.Close()

	log.Println("Consumidor: Iniciado. Aguardando dados...")
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigchan:
			log.Println("Consumidor: Encerrando...")
			return
		default:
			m, err := kafkaReader.ReadMessage(context.Background())
			if err != nil {
				continue
			}

			var data TransactionData
			if err := json.Unmarshal(m.Value, &data); err != nil {
				continue
			}

			sqlStatement := `INSERT INTO transactions (hash, to_address, from_address, "inputData", event_timestamp) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (hash) DO NOTHING;`

			_, err = db.Exec(sqlStatement, data.Hash, data.To, data.From, data.InputData, time.Unix(data.Timestamp, 0))
			if err != nil {
				log.Printf("!!! CONSUMIDOR: Erro ao inserir no DB: %v", err)
			} else {
				log.Printf("[DADO SALVO] Hash: %s", data.Hash)
			}
		}
	}
}
