package auth

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

func InitDB() error {
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	if dbHost == "" {
		// Fallback for local development if env not set, though docker-compose sets it
		dbHost = "127.0.0.1"
		dbUser = "rester"
		dbPass = "rester_secret"
		dbName = "rester_db"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:3306)/%s?parseTime=true", dbUser, dbPass, dbHost, dbName)

	var err error
	// Retry connection loop
	for i := 0; i < 10; i++ {
		DB, err = sql.Open("mysql", dsn)
		if err == nil {
			err = DB.Ping()
			if err == nil {
				break
			}
		}
		log.Printf("InitDB: Waiting for Database (%s)... %v", dbHost, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	log.Println("InitDB: Connected to Database")
	return migrate()
}

func migrate() error {
	// Users Table
	queryUsers := `
	CREATE TABLE IF NOT EXISTS users (
		id INT AUTO_INCREMENT PRIMARY KEY,
		username VARCHAR(255) NOT NULL UNIQUE,
		password_hash VARCHAR(255) NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := DB.Exec(queryUsers); err != nil {
		return fmt.Errorf("migration (users) failed: %w", err)
	}

	// Logs Table
	queryLogs := `
	CREATE TABLE IF NOT EXISTS logs (
		id INT AUTO_INCREMENT PRIMARY KEY,
		level VARCHAR(10) NOT NULL,
		message TEXT NOT NULL,
		details TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := DB.Exec(queryLogs); err != nil {
		return fmt.Errorf("migration (logs) failed: %w", err)
	}

	// Snapshot Cache Table
	queryCache := `
	CREATE TABLE IF NOT EXISTS snapshot_cache (
		snapshot_id VARCHAR(64) PRIMARY KEY,
		repo_name VARCHAR(255) NOT NULL,
		size BIGINT DEFAULT 0,
		file_count INT DEFAULT 0,
		duration VARCHAR(50),
		processed BOOLEAN DEFAULT FALSE,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := DB.Exec(queryCache); err != nil {
		return fmt.Errorf("migration (snapshot_cache) failed: %w", err)
	}

	return nil
}
