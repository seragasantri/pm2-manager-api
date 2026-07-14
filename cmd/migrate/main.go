package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/tragasolusi/pm2-manager-api/internal/config"
)

func main() {
	cfg := config.Load()

	db, err := sql.Open("mysql", cfg.DB.DSN())
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping: %v", err)
	}

	steps := []struct {
		name string
		sql  string
	}{
		{
			"users",
			`CREATE TABLE IF NOT EXISTS users (
				id INT AUTO_INCREMENT PRIMARY KEY,
				username VARCHAR(100) NOT NULL UNIQUE,
				password VARCHAR(255) NOT NULL,
				role ENUM('superadmin','user') DEFAULT 'user',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		},
		{
			"apps",
			`CREATE TABLE IF NOT EXISTS apps (
				id INT AUTO_INCREMENT PRIMARY KEY,
				name VARCHAR(100) NOT NULL UNIQUE,
				cwd VARCHAR(255) DEFAULT '',
				pm_id VARCHAR(64) DEFAULT '',
				description TEXT,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		},
		{
			"apps.pm_id type",
			`ALTER TABLE apps MODIFY COLUMN pm_id VARCHAR(64) DEFAULT ''`,
		},
		{
			"tokens",
			`CREATE TABLE IF NOT EXISTS tokens (
				id INT AUTO_INCREMENT PRIMARY KEY,
				code VARCHAR(64) NOT NULL UNIQUE,
				label VARCHAR(255) NOT NULL,
				allowed_apps JSON NOT NULL,
				created_by INT,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
				FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		},
	}

	log.Println("Migrasi database dimulai...")
	for _, s := range steps {
		if _, err := db.ExecContext(ctx, s.sql); err != nil {
			log.Fatalf("[FAIL] %s: %v", s.name, err)
		}
		fmt.Printf("[OK] %s\n", s.name)
	}
	log.Println("Migrasi selesai!")
}
