package main

import (
	"context"
	"database/sql"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"

	"github.com/tragasolusi/pm2-manager-api/internal/config"
)

func main() {
	cfg := config.Load()
	if cfg.SuperAdmin.Username == "" || cfg.SuperAdmin.Password == "" {
		log.Fatal("SUPERADMIN_USERNAME dan SUPERADMIN_PASSWORD harus di-set di .env")
	}

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

	// Seed super admin
	hashed, err := bcrypt.GenerateFromPassword([]byte(cfg.SuperAdmin.Password), 10)
	if err != nil {
		log.Fatalf("hash: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT IGNORE INTO users (username, password, role) VALUES (?, ?, 'superadmin')`,
		cfg.SuperAdmin.Username, string(hashed),
	); err != nil {
		log.Fatalf("insert superadmin: %v", err)
	}
	log.Printf("[OK] Super admin '%s' sudah dibuat", cfg.SuperAdmin.Username)

	// Sample apps
	apps := []struct{ name, desc string }{
		{"pm2-ui", "PM2 Manager Frontend"},
		{"pm2-manager-api", "PM2 Manager Backend API"},
	}
	for _, a := range apps {
		if _, err := db.ExecContext(ctx,
			`INSERT IGNORE INTO apps (name, description) VALUES (?, ?)`,
			a.name, a.desc,
		); err != nil {
			log.Fatalf("insert app %s: %v", a.name, err)
		}
		log.Printf("[OK] App '%s' sudah dibuat", a.name)
	}

	log.Println("Seeder selesai!")
}
