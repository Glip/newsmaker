package templates

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Template struct {
	ID        int64
	Name      string
	Body      string
	CreatedAt string
	UpdatedAt string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) List() ([]Template, error) {
	rows, err := s.db.Query(`SELECT id, name, body, created_at, updated_at FROM templates ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Body, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Get(id int64) (Template, error) {
	var t Template
	err := s.db.QueryRow(`SELECT id, name, body, created_at, updated_at FROM templates WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.Body, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func (s *Store) Create(name, body string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("name is required")
	}
	res, err := s.db.Exec(`INSERT INTO templates(name, body) VALUES(?,?)`, name, body)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Update(id int64, name, body string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	_, err := s.db.Exec(`UPDATE templates SET name=?, body=?, updated_at=? WHERE id=?`,
		name, body, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM templates WHERE id=?`, id)
	return err
}

func Apply(body string, vars map[string]string) string {
	out := body
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}
