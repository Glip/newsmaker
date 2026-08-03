package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const cookieName = "newsmaker_session"

type Service struct {
	username string
	hash     []byte
	mu       sync.Mutex
	sessions map[string]time.Time
	ttl      time.Duration
}

func New(username, password string) (*Service, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &Service{
		username: username,
		hash:     hash,
		sessions: make(map[string]time.Time),
		ttl:      7 * 24 * time.Hour,
	}, nil
}

func (s *Service) Authenticate(username, password string) bool {
	if subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) != 1 {
		return false
	}
	return bcrypt.CompareHashAndPassword(s.hash, []byte(password)) == nil
}

func (s *Service) CreateSession(w http.ResponseWriter) error {
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(s.ttl)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.ttl.Seconds()),
	})
	return nil
}

func (s *Service) ClearSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (s *Service) Valid(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.sessions, c.Value)
		return false
	}
	return true
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Valid(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) ChangePassword(current, nextPassword string) error {
	if bcrypt.CompareHashAndPassword(s.hash, []byte(current)) != nil {
		return ErrBadPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(nextPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.hash = hash
	return nil
}

var ErrBadPassword = errString("invalid current password")

type errString string

func (e errString) Error() string { return string(e) }

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
