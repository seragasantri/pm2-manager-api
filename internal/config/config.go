package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// AppConfig holds all runtime configuration loaded from env vars.
type AppConfig struct {
	Port      string
	NodeEnv   string
	DB        DBConfig
	JWT       JWTConfig
	SuperAdmin SuperAdminConfig
	PM2       PM2Config
}

type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
}

type JWTConfig struct {
	Secret    string
	ExpiresIn time.Duration
}

type SuperAdminConfig struct {
	Username string
	Password string
}

type PM2Config struct {
	Home  string // PM2_HOME
	Bin   string // path to pm2 binary
}

// DSN returns the MySQL DSN string.
func (d DBConfig) DSN() string {
	return fmt.Sprintf(
		"%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci",
		d.User, d.Password, d.Host, d.Port, d.Name,
	)
}

// Load reads .env (if present) and returns AppConfig.
func Load() *AppConfig {
	_ = godotenv.Load()

	dbPort, _ := strconv.Atoi(getEnv("DB_PORT", "3306"))

	jwtExpires := getEnv("JWT_EXPIRES_IN", "24h")
	jwtDur, err := time.ParseDuration(jwtExpires)
	if err != nil {
		jwtDur = 24 * time.Hour
	}

	return &AppConfig{
		Port:    getEnv("PORT", "3003"),
		NodeEnv: getEnv("NODE_ENV", "development"),
		DB: DBConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     dbPort,
			User:     getEnv("DB_USER", "root"),
			Password: getEnv("DB_PASSWORD", ""),
			Name:     getEnv("DB_NAME", "pm2_manager"),
		},
		JWT: JWTConfig{
			Secret:    getEnv("JWT_SECRET", ""),
			ExpiresIn: jwtDur,
		},
		SuperAdmin: SuperAdminConfig{
			Username: getEnv("SUPERADMIN_USERNAME", ""),
			Password: getEnv("SUPERADMIN_PASSWORD", ""),
		},
		PM2: PM2Config{
			Home: getEnv("PM2_HOME", "/root/.pm2"),
			Bin:  getEnv("PM2_BIN", "pm2"),
		},
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
