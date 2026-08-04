package schedule

import (
	"crypto/rand"
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
	ID             string
	CreatedAt      string
	SendAt         time.Time
	Status         string
	Text           string
	UseSignature   bool
	ChannelIDs     []int64
	Media          []MediaItem
	PreviewText    string
	JobID          string
	Error          string
	SeriesID       string
	RepeatKind     string
	RepeatWeekdays []int
	RepeatUntil    time.Time // zero = none
}

func (p Post) RepeatLabel() string {
	return RepeatLabel(p.RepeatKind, p.RepeatWeekdays)
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
	if err != nil {
		return err
	}
	for _, col := range []struct {
		name, def string
	}{
		{"series_id", "TEXT NOT NULL DEFAULT ''"},
		{"repeat_kind", "TEXT NOT NULL DEFAULT ''"},
		{"repeat_weekdays", "TEXT NOT NULL DEFAULT '[]'"},
		{"repeat_until", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureColumn(db, "scheduled_posts", col.name, col.def); err != nil {
			return err
		}
	}
	_, err = db.Exec(`
CREATE INDEX IF NOT EXISTS idx_scheduled_posts_series
  ON scheduled_posts(series_id, status);
`)
	return err
}

func ensureColumn(db *sql.DB, table, name, def string) error {
	// Identifiers only — not user input.
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var colName, colType string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &colName, &colType, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if colName == name {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, def))
	return err
}

const postSelectCols = `id, created_at, send_at, status, text, use_signature, channel_ids_json, media_json, preview_text, job_id, error, series_id, repeat_kind, repeat_weekdays, repeat_until`

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
	if p.RepeatWeekdays == nil {
		p.RepeatWeekdays = []int{}
	}
	wdJSON, err := json.Marshal(p.RepeatWeekdays)
	if err != nil {
		return err
	}
	useSig := 0
	if p.UseSignature {
		useSig = 1
	}
	until := ""
	if !p.RepeatUntil.IsZero() {
		until = p.RepeatUntil.UTC().Format(time.RFC3339)
	}
	_, err = s.db.Exec(`
INSERT INTO scheduled_posts(
  id, send_at, status, text, use_signature, channel_ids_json, media_json, preview_text,
  series_id, repeat_kind, repeat_weekdays, repeat_until
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID,
		p.SendAt.UTC().Format(time.RFC3339),
		p.Status,
		p.Text,
		useSig,
		string(chJSON),
		string(mediaJSON),
		p.PreviewText,
		p.SeriesID,
		p.RepeatKind,
		string(wdJSON),
		until,
	)
	return err
}

// CreateSeries materializes pending occurrences for a recurring rule.
func (s *Store) CreateSeries(base Post) (seriesID string, count int, err error) {
	if base.RepeatKind != RepeatWeekly && base.RepeatKind != RepeatMonthly {
		return "", 0, fmt.Errorf("invalid repeat kind")
	}
	times, err := NextOccurrences(base.RepeatKind, base.RepeatWeekdays, base.SendAt, base.RepeatUntil)
	if err != nil {
		return "", 0, err
	}
	if len(times) == 0 {
		return "", 0, fmt.Errorf("нет дат для повторения в выбранном диапазоне")
	}
	seriesID = newID()
	for _, t := range times {
		p := base
		p.ID = newID()
		p.SendAt = t
		p.Status = StatusPending
		p.SeriesID = seriesID
		if err := s.Create(p); err != nil {
			return seriesID, count, err
		}
		count++
	}
	return seriesID, count, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Store) ListUpcoming(limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT `+postSelectCols+`
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
SELECT `+postSelectCols+`
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

func (s *Store) CancelSeries(seriesID string) (int64, error) {
	seriesID = strings.TrimSpace(seriesID)
	if seriesID == "" {
		return 0, fmt.Errorf("series_id required")
	}
	res, err := s.db.Exec(`UPDATE scheduled_posts SET status=? WHERE series_id=? AND status=?`, StatusCancelled, seriesID, StatusPending)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

// ListBetween returns posts with send_at in [from, to) (UTC RFC3339 compare).
func (s *Store) ListBetween(from, to time.Time) ([]Post, error) {
	rows, err := s.db.Query(`
SELECT `+postSelectCols+`
FROM scheduled_posts
WHERE send_at >= ? AND send_at < ?
ORDER BY send_at ASC`, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// ListRecent returns recent scheduled posts of any status for the schedule page.
func (s *Store) ListRecent(limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 80
	}
	rows, err := s.db.Query(`
SELECT `+postSelectCols+`
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
		var chJSON, mediaJSON, wdJSON, until string
		if err := rows.Scan(
			&p.ID, &p.CreatedAt, &sendAt, &p.Status, &p.Text, &useSig,
			&chJSON, &mediaJSON, &p.PreviewText, &p.JobID, &p.Error,
			&p.SeriesID, &p.RepeatKind, &wdJSON, &until,
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
		_ = json.Unmarshal([]byte(wdJSON), &p.RepeatWeekdays)
		if until != "" {
			if ut, err := time.Parse(time.RFC3339, until); err == nil {
				p.RepeatUntil = ut.UTC()
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
