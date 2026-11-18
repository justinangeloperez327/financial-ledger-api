package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config contains the minimal settings required to run the API.
type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   string
	JWTIssuer   string
	JWTAudience string
}

func Load() (Config, error) {
	port := strings.TrimSpace(os.Getenv("APP_PORT"))
	if port == "" {
		port = "8080"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return Config{}, fmt.Errorf("invalid APP_PORT %q: must be 1-65535", port)
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return Config{}, errors.New("JWT_SECRET must contain at least 32 bytes")
	}
	issuer := strings.TrimSpace(os.Getenv("JWT_ISSUER"))
	if issuer == "" {
		issuer = "financial-ledger-api"
	}
	audience := strings.TrimSpace(os.Getenv("JWT_AUDIENCE"))
	if audience == "" {
		audience = "financial-ledger-api"
	}
	return Config{Port: port, DatabaseURL: databaseURL, JWTSecret: secret, JWTIssuer: issuer, JWTAudience: audience}, nil
}
