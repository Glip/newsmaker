package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"newsmaker/internal/auth"
	"newsmaker/internal/channels"
	"newsmaker/internal/config"
	"newsmaker/internal/cryptoutil"
	"newsmaker/internal/db"
	"newsmaker/internal/history"
	"newsmaker/internal/httpserver"
	"newsmaker/internal/media"
	"newsmaker/internal/publish"
	"newsmaker/internal/publish/discord"
	"newsmaker/internal/publish/lolka"
	maxbot "newsmaker/internal/publish/max"
	"newsmaker/internal/publish/telegram"
	"newsmaker/internal/publish/vk"
	"newsmaker/internal/settings"
	"newsmaker/internal/templates"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	sqlDB, err := db.Open(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer sqlDB.Close()
	if err := history.EnsureSchema(sqlDB); err != nil {
		log.Fatal(err)
	}

	box, err := cryptoutil.NewBox(cfg.Secret)
	if err != nil {
		log.Fatal(err)
	}
	authSvc, err := auth.New(cfg.User, cfg.Password)
	if err != nil {
		log.Fatal(err)
	}

	chStore := channels.NewStore(sqlDB, box)
	tplStore := templates.NewStore(sqlDB)
	setStore := settings.NewStore(sqlDB)
	histStore := history.NewStore(sqlDB)

	proc := &media.Processor{
		FFmpeg: cfg.FFmpeg,
		TmpDir: filepath.Join(cfg.DataDir, "tmp"),
	}
	dispatch := &publish.Dispatcher{
		Media: proc,
		Publishers: map[channels.Platform]publish.Publisher{
			channels.PlatformTelegram: &telegram.Publisher{Client: &http.Client{Timeout: 120 * time.Second}},
			channels.PlatformDiscord:  &discord.Publisher{Client: &http.Client{Timeout: 120 * time.Second}},
			channels.PlatformLolka:    &lolka.Publisher{Client: &http.Client{Timeout: 120 * time.Second}},
			channels.PlatformMAX:      &maxbot.Publisher{Client: &http.Client{Timeout: 120 * time.Second}},
			channels.PlatformVK:       &vk.Publisher{Client: &http.Client{Timeout: 180 * time.Second}},
		},
	}

	srv, err := httpserver.New(cfg, sqlDB, authSvc, chStore, tplStore, setStore, histStore, dispatch)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("newsmaker listening on %s (data=%s)", cfg.Addr, cfg.DataDir)
	if err := http.ListenAndServe(cfg.Addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
