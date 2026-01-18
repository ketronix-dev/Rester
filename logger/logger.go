package logger

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

var DB *sql.DB

type LogEntry struct {
	ID        int       `json:"id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func Init(db *sql.DB) {
	DB = db
}

func logMessage(level, message, details string) {
	// Stdout fallback
	log.Printf("[%s] %s %s", level, message, details)

	if DB == nil {
		return
	}

	_, err := DB.Exec("INSERT INTO logs (level, message, details) VALUES (?, ?, ?)", level, message, details)
	if err != nil {
		log.Printf("Failed to insert log: %v", err)
	}
}

func Info(message string, args ...interface{}) {
	msg := fmt.Sprintf(message, args...)
	logMessage("INFO", msg, "")
}

func Warn(message string, args ...interface{}) {
	msg := fmt.Sprintf(message, args...)
	logMessage("WARN", msg, "")
}

func Error(message string, args ...interface{}) {
	msg := fmt.Sprintf(message, args...)
	logMessage("ERROR", msg, "")
}

func Debug(message string, args ...interface{}) {
	msg := fmt.Sprintf(message, args...)
	logMessage("DEBUG", msg, "")
}

func GetLogs(limit int) ([]LogEntry, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	rows, err := DB.Query("SELECT id, level, message, details, created_at FROM logs ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var l LogEntry
		if err := rows.Scan(&l.ID, &l.Level, &l.Message, &l.Details, &l.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, l)
	}
	return logs, nil
}
