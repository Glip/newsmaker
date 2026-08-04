package schedule

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	StatusPending   = "pending"
	StatusSending   = "sending"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type MediaItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
}

type Post struct {
	ID           string
	CreatedAt    string
	SendAt       time.Time
	Status       string
	Text         string
	UseSignature bool
	ChannelIDs   []int64
	Media        []MediaItem
	PreviewText  string
	JobID        string
	Error        string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS scheduled_posts (
  id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  send_at TEXT NOT NULL,
  status TEXT NOT NULL,
  text TEXT NOT NULL,
  use_signature INTEGER NOT NULL DEFAULT 0,
  channel_ids_json TEXT NOT NULL,
  media_json TEXT NOT NULL DEFAULT '[]',
  preview_text TEXT NOT NULL DEFAULT '',
  job_id TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_scheduled_posts_due
  ON scheduled_posts(status, send_at);
`)
	return err
}

func (s *Store) Create(p Post) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("id required")
	}
	if p.SendAt.IsZero() {
		return fmt.Errorf("send_at required")
	}
	if p.Status == "" {
		p.Status = StatusPending
	}
	chJSON, err := json.Marshal(p.ChannelIDs)
	if err != nil {
		return err
	}
	mediaJSON, err := json.Marshal(p.Media)
	if err != nil {
		return err
	}
	useSig := 0
	if p.UseSignature {
		useSig = 1
	}
	_, err = s.db.Exec(`
INSERT INTO scheduled_posts(
  id, send_at, status, text, use_signature, channel_ids_json, media_json, preview_text
) VALUES(?,?,?,?,?,?,?,?)`,
		p.ID,
		p.SendAt.UTC().Format(time.RFC3339),
		p.Status,
		p.Text,
		useSig,
		string(chJSON),
		string(mediaJSON),
		p.PreviewText,
	)
	return err
}

func (s *Store) ListUpcoming(limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT id, created_at, send_at, status, text, use_signature, channel_ids_json, media_json, preview_text, job_id, error
FROM scheduled_posts
WHERE status IN (?, ?)
ORDER BY send_at ASC
LIMIT ?`, StatusPending, StatusSending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

func (s *Store) ListDue(now time.Time, limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
SELECT id, created_at, send_at, status, text, use_signature, channel_ids_json, media_json, preview_text, job_id, error
FROM scheduled_posts
WHERE status=? AND send_at<=?
ORDER BY send_at ASC
LIMIT ?`, StatusPending, now.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

func (s *Store) Cancel(id string) error {
	res, err := s.db.Exec(`UPDATE scheduled_posts SET status=? WHERE id=? AND status=?`, StatusCancelled, id, StatusPending)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("не найдено или уже не pending")
	}
	return nil
}

func (s *Store) Claim(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE scheduled_posts SET status=? WHERE id=? AND status=?`, StatusSending, id, StatusPending)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) MarkDone(id, jobID string) error {
	_, err := s.db.Exec(`UPDATE scheduled_posts SET status=?, job_id=?, error='' WHERE id=?`, StatusDone, jobID, id)
	return err
}

func (s *Store) MarkFailed(id, errMsg string) error {
	_, err := s.db.Exec(`UPDATE scheduled_posts SET status=?, error=? WHERE id=?`, StatusFailed, errMsg, id)
	return err
}

// RecoverStuckSending moves interrupted "sending" rows back to pending (e.g. after restart).
func (s *Store) RecoverStuckSending() (int64, error) {
	res, err := s.db.Exec(`UPDATE scheduled_posts SET status=? WHERE status=?`, StatusPending, StatusSending)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListRecent returns recent scheduled posts of any status for the schedule page.
func (s *Store) ListRecent(limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 80
	}
	rows, err := s.db.Query(`
SELECT id, created_at, send_at, status, text, use_signature, channel_ids_json, media_json, preview_text, job_id, error
FROM scheduled_posts
ORDER BY
  CASE status
    WHEN 'pending' THEN 0
    WHEN 'sending' THEN 1
    ELSE 2
  END,
  send_at DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

func scanPosts(rows *sql.Rows) ([]Post, error) {
	var out []Post
	for rows.Next() {
		var p Post
		var sendAt string
		var useSig int
		var chJSON, mediaJSON string
		if err := rows.Scan(
			&p.ID, &p.CreatedAt, &sendAt, &p.Status, &p.Text, &useSig,
			&chJSON, &mediaJSON, &p.PreviewText, &p.JobID, &p.Error,
		); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, sendAt)
		if err != nil {
			t, _ = time.Parse("2006-01-02 15:04:05", sendAt)
		}
		p.SendAt = t.UTC()
		p.UseSignature = useSig == 1
		_ = json.Unmarshal([]byte(chJSON), &p.ChannelIDs)
		_ = json.Unmarshal([]byte(mediaJSON), &p.Media)
		out = append(out, p)
	}
	return out, rows.Err()
}
