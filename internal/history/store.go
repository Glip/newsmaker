package history

import (
	"database/sql"
	"fmt"
)

type Result struct {
	ID          int64
	ChannelID   int64
	Platform    string
	ChannelName string
	OK          bool
	MessageRef  string
	PostURL     string
	Error       string
}

type Job struct {
	ID          string
	CreatedAt   string
	Status      string
	PreviewText string
	Results     []Result
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func EnsureSchema(db *sql.DB) error {
	if err := addColumnIfMissing(db, "send_jobs", "preview_text", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "send_results", "post_url", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func addColumnIfMissing(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	return err
}

func (s *Store) List(limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, created_at, status, preview_text FROM send_jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	idx := map[string]int{}
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.CreatedAt, &j.Status, &j.PreviewText); err != nil {
			return nil, err
		}
		idx[j.ID] = len(jobs)
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return jobs, nil
	}

	rrows, err := s.db.Query(`
SELECT job_id, id, channel_id, platform, channel_name, ok, message_ref, COALESCE(post_url,''), error
FROM send_results
WHERE job_id IN (` + placeholders(len(jobs)) + `)
ORDER BY id ASC`, jobIDs(jobs)...)
	if err != nil {
		return nil, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var jobID string
		var r Result
		var ok int
		if err := rrows.Scan(&jobID, &r.ID, &r.ChannelID, &r.Platform, &r.ChannelName, &ok, &r.MessageRef, &r.PostURL, &r.Error); err != nil {
			return nil, err
		}
		r.OK = ok == 1
		if i, exists := idx[jobID]; exists {
			jobs[i].Results = append(jobs[i].Results, r)
		}
	}
	return jobs, rrows.Err()
}

func placeholders(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += "?"
	}
	return s
}

func jobIDs(jobs []Job) []any {
	out := make([]any, len(jobs))
	for i := range jobs {
		out[i] = jobs[i].ID
	}
	return out
}
