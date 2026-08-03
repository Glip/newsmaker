package channels

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"newsmaker/internal/cryptoutil"
)

// Discord / Lolka Target ID: permalink to any message in the webhook's channel
// (Copy Message Link → server_id/channel_id for history URLs).
var (
	reDiscordChannelLink = regexp.MustCompile(`(?i)(?:https?://)?(?:ptb\.|canary\.)?discord(?:app)?\.com/channels/\d+/\d+`)
	reLolkaChannelLink   = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?lolka\.app/channels/\d+/\d+`)
	reLolkaWebhookURL    = regexp.MustCompile(`(?i)^https://(?:www\.)?lolka\.app/api/(?:bot/v\d+/)?webhooks/\d+/`)
)

type Platform string

const (
	PlatformTelegram Platform = "telegram"
	PlatformDiscord  Platform = "discord"
	PlatformLolka    Platform = "lolka"
	PlatformMAX      Platform = "max"
	PlatformVK       Platform = "vk"
)

type Channel struct {
	ID         int64
	Platform   Platform
	Name       string
	TargetID   string
	Credential string // decrypted
	Enabled    bool
	CreatedAt  string
	UpdatedAt  string
}

type Store struct {
	db  *sql.DB
	box *cryptoutil.Box
}

func NewStore(db *sql.DB, box *cryptoutil.Box) *Store {
	return &Store{db: db, box: box}
}

func ValidPlatform(p string) bool {
	switch Platform(p) {
	case PlatformTelegram, PlatformDiscord, PlatformLolka, PlatformMAX, PlatformVK:
		return true
	default:
		return false
	}
}

func (s *Store) List() ([]Channel, error) {
	rows, err := s.db.Query(`SELECT id, platform, name, target_id, credential_enc, enabled, created_at, updated_at FROM channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		var enc string
		var enabled int
		if err := rows.Scan(&c.ID, &c.Platform, &c.Name, &c.TargetID, &enc, &enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled == 1
		cred, err := s.box.Open(enc)
		if err != nil {
			return nil, fmt.Errorf("decrypt channel %d: %w", c.ID, err)
		}
		c.Credential = cred
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Get(id int64) (Channel, error) {
	var c Channel
	var enc string
	var enabled int
	err := s.db.QueryRow(`SELECT id, platform, name, target_id, credential_enc, enabled, created_at, updated_at FROM channels WHERE id=?`, id).
		Scan(&c.ID, &c.Platform, &c.Name, &c.TargetID, &enc, &enabled, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	c.Enabled = enabled == 1
	cred, err := s.box.Open(enc)
	if err != nil {
		return c, err
	}
	c.Credential = cred
	return c, nil
}

func (s *Store) Create(c Channel) (int64, error) {
	if err := validate(c); err != nil {
		return 0, err
	}
	enc, err := s.box.Seal(c.Credential)
	if err != nil {
		return 0, err
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	res, err := s.db.Exec(`INSERT INTO channels(platform, name, target_id, credential_enc, enabled) VALUES(?,?,?,?,?)`,
		c.Platform, strings.TrimSpace(c.Name), strings.TrimSpace(c.TargetID), enc, enabled)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Update(c Channel) error {
	if err := validate(c); err != nil {
		return err
	}
	enc, err := s.box.Seal(c.Credential)
	if err != nil {
		return err
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err = s.db.Exec(`UPDATE channels SET platform=?, name=?, target_id=?, credential_enc=?, enabled=?, updated_at=? WHERE id=?`,
		c.Platform, strings.TrimSpace(c.Name), strings.TrimSpace(c.TargetID), enc, enabled, time.Now().UTC().Format(time.RFC3339), c.ID)
	return err
}

func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	return err
}

func validate(c Channel) error {
	if !ValidPlatform(string(c.Platform)) {
		return fmt.Errorf("unsupported platform")
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(c.Credential) == "" {
		return fmt.Errorf("credential is required")
	}
	switch c.Platform {
	case PlatformTelegram, PlatformMAX, PlatformVK:
		if strings.TrimSpace(c.TargetID) == "" {
			return fmt.Errorf("target id is required for %s", c.Platform)
		}
	case PlatformDiscord:
		t := strings.TrimSpace(c.TargetID)
		if t == "" {
			return fmt.Errorf("discord: укажите ссылку на любое сообщение канала (ПКМ → Copy Message Link)")
		}
		if !reDiscordChannelLink.MatchString(t) {
			return fmt.Errorf("discord: ожидается ссылка вида https://discord.com/channels/server/channel/message")
		}
	case PlatformLolka:
		t := strings.TrimSpace(c.TargetID)
		if t == "" {
			return fmt.Errorf("lolka: укажите ссылку на любое сообщение канала (ПКМ → копировать ссылку)")
		}
		if !reLolkaChannelLink.MatchString(t) {
			return fmt.Errorf("lolka: ожидается ссылка вида https://lolka.app/channels/server/channel/message")
		}
		cred := strings.TrimSpace(c.Credential)
		if !reLolkaWebhookURL.MatchString(cred) {
			return fmt.Errorf("lolka: credential — URL вебхука https://lolka.app/api/webhooks/id/token")
		}
	}
	return nil
}
