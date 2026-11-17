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

	return Config{Port: port, DatabaseURL: databaseURL}, nil
}
