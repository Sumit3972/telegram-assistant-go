package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"os"
)

func main() {
	_ = godotenv.Load(".env")
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	cmdTag, err := pool.Exec(ctx, "DELETE FROM chat_history;")
	if err != nil {
		log.Fatalf("Failed to clear chat_history: %v", err)
	}

	fmt.Printf("✅ Successfully cleared all chat records! Deleted rows: %d\n", cmdTag.RowsAffected())
}
