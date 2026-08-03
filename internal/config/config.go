package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Addr     string
	User     string
	Password string
	Secret   string
	DataDir  string
	WebDir   string
	FFmpeg   string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:     getenv("NEWSMAKER_ADDR", ":8080"),
		User:     getenv("NEWSMAKER_USER", "admin"),
		Password: os.Getenv("NEWSMAKER_PASSWORD"),
		Secret:   os.Getenv("NEWSMAKER_SECRET"),
		DataDir:  getenv("DATA_DIR", "data"),
		WebDir:   getenv("WEB_DIR", "web"),
		FFmpeg:   getenv("FFMPEG_PATH", "ffmpeg"),
	}
	if strings.TrimSpace(cfg.Password) == "" {
		return cfg, fmt.Errorf("NEWSMAKER_PASSWORD is required")
	}
	if len(cfg.Secret) < 16 {
		return cfg, fmt.Errorf("NEWSMAKER_SECRET must be at least 16 characters")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
