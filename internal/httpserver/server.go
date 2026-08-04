package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"newsmaker/internal/auth"
	"newsmaker/internal/channels"
	"newsmaker/internal/config"
	"newsmaker/internal/format"
	"newsmaker/internal/history"
	"newsmaker/internal/media"
	"newsmaker/internal/publish"
	"newsmaker/internal/schedule"
	"newsmaker/internal/settings"
	"newsmaker/internal/templates"
)

type Server struct {
	cfg       config.Config
	auth      *auth.Service
	channels  *channels.Store
	templates *templates.Store
	settings  *settings.Store
	history   *history.Store
	schedule  *schedule.Store
	dispatch  *publish.Dispatcher
	db        *sql.DB
	tpl       *template.Template
	uploads   string
	tmp       string
}

func New(
	cfg config.Config,
	db *sql.DB,
	authSvc *auth.Service,
	ch *channels.Store,
	tplStore *templates.Store,
	set *settings.Store,
	hist *history.Store,
	sched *schedule.Store,
	dispatch *publish.Dispatcher,
) (*Server, error) {
	uploads := filepath.Join(cfg.DataDir, "uploads")
	tmp := filepath.Join(cfg.DataDir, "tmp")
	_ = os.MkdirAll(uploads, 0o755)
	_ = os.MkdirAll(tmp, 0o755)

	funcs := template.FuncMap{
		"platformLabel": func(v any) string {
			switch t := v.(type) {
			case channels.Platform:
				return platformLabelStr(string(t))
			case string:
				return platformLabelStr(t)
			default:
				return platformLabelStr(fmt.Sprint(v))
			}
		},
		"yesno": func(b bool) string {
			if b {
				return "да"
			}
			return "нет"
		},
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("02.01.2006 15:04")
		},
		"fmtClock": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("15:04")
		},
	}
	tpl, err := template.New("").Funcs(funcs).ParseGlob(filepath.Join(cfg.WebDir, "templates", "*.html"))
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:       cfg,
		auth:      authSvc,
		channels:  ch,
		templates: tplStore,
		settings:  set,
		history:   hist,
		schedule:  sched,
		dispatch:  dispatch,
		db:        db,
		tpl:       tpl,
		uploads:   uploads,
		tmp:       tmp,
	}, nil
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	static := filepath.Join(s.cfg.WebDir, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir(static))))

	r.Get("/login", s.loginPage)
	r.Post("/login", s.loginSubmit)
	r.Post("/logout", s.logout)

	r.Group(func(pr chi.Router) {
		pr.Use(s.auth.Middleware)
		pr.Get("/", s.composePage)
		pr.Get("/channels", s.channelsPage)
		pr.Post("/channels", s.channelCreate)
		pr.Post("/channels/{id}/update", s.channelUpdate)
		pr.Post("/channels/{id}/delete", s.channelDelete)
		pr.Get("/templates", s.templatesPage)
		pr.Post("/templates", s.templateCreate)
		pr.Post("/templates/{id}/delete", s.templateDelete)
		pr.Get("/settings", s.settingsPage)
		pr.Post("/settings", s.settingsSave)
		pr.Get("/history", s.historyPage)
		pr.Get("/schedule", s.schedulePage)
		pr.Post("/schedule/{id}/cancel", s.scheduleCancel)

		pr.Post("/api/upload", s.apiUpload)
		pr.Get("/api/uploads/{name}", s.apiServeUpload)
		pr.Post("/api/preview", s.apiPreview)
		pr.Post("/api/send", s.apiSend)
		pr.Post("/api/schedule", s.apiSchedule)
	})
	return r
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.auth.Valid(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login.html", map[string]any{"Error": r.URL.Query().Get("e")})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	user := r.FormValue("username")
	pass := r.FormValue("password")
	if !s.auth.Authenticate(user, pass) {
		http.Redirect(w, r, "/login?e=1", http.StatusSeeOther)
		return
	}
	if err := s.auth.CreateSession(w); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.auth.ClearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) composePage(w http.ResponseWriter, r *http.Request) {
	chs, _ := s.channels.List()
	tpls, _ := s.templates.List()
	sig := s.settings.Get(settings.KeySignature, "")
	s.render(w, "compose.html", map[string]any{
		"Title":     "Compose",
		"Channels":  chs,
		"Templates": tpls,
		"Signature": sig,
	})
}

func (s *Server) channelsPage(w http.ResponseWriter, r *http.Request) {
	chs, err := s.channels.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "channels.html", map[string]any{
		"Title":    "Каналы",
		"Channels": chs,
		"Error":    r.URL.Query().Get("e"),
	})
}

func (s *Server) channelCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	c := channels.Channel{
		Platform:   channels.Platform(r.FormValue("platform")),
		Name:       r.FormValue("name"),
		TargetID:   r.FormValue("target_id"),
		Credential: r.FormValue("credential"),
		Enabled:    r.FormValue("enabled") == "on" || r.FormValue("enabled") == "1",
	}
	if _, err := s.channels.Create(c); err != nil {
		http.Redirect(w, r, "/channels?e="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

func (s *Server) channelUpdate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	existing, err := s.channels.Get(id)
	if err != nil {
		http.Redirect(w, r, "/channels?e="+url.QueryEscape("канал не найден"), http.StatusSeeOther)
		return
	}
	existing.Name = r.FormValue("name")
	existing.TargetID = r.FormValue("target_id")
	if cred := strings.TrimSpace(r.FormValue("credential")); cred != "" {
		existing.Credential = cred
	}
	existing.Enabled = r.FormValue("enabled") == "on" || r.FormValue("enabled") == "1"
	if err := s.channels.Update(existing); err != nil {
		http.Redirect(w, r, "/channels?e="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

func (s *Server) channelDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.channels.Delete(id)
	http.Redirect(w, r, "/channels", http.StatusSeeOther)
}

func (s *Server) templatesPage(w http.ResponseWriter, r *http.Request) {
	tpls, err := s.templates.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "templates.html", map[string]any{
		"Title":     "Шаблоны",
		"Templates": tpls,
	})
}

func (s *Server) templateCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if _, err := s.templates.Create(r.FormValue("name"), r.FormValue("body")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (s *Server) templateDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.templates.Delete(id)
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "settings.html", map[string]any{
		"Title":     "Настройки",
		"Signature": s.settings.Get(settings.KeySignature, ""),
		"Msg":       r.URL.Query().Get("msg"),
	})
}

func (s *Server) historyPage(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.history.List(80)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "history.html", map[string]any{
		"Title": "История",
		"Jobs":  jobs,
	})
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	_ = s.settings.Set(settings.KeySignature, r.FormValue("signature"))
	cur := r.FormValue("current_password")
	next := r.FormValue("new_password")
	if cur != "" || next != "" {
		if err := s.auth.ChangePassword(cur, next); err != nil {
			http.Redirect(w, r, "/settings?msg="+err.Error(), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/settings?msg=saved", http.StatusSeeOther)
}

func (s *Server) apiUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "file required"})
		return
	}
	defer file.Close()
	id := uuid.NewString()
	ext := filepath.Ext(hdr.Filename)
	dst := filepath.Join(s.uploads, id+ext)
	n, err := media.CopyUpload(file, dst)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	kind := media.KindPhoto
	ct := hdr.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "video/") || isVideoExt(ext):
		kind = media.KindVideo
	case strings.HasPrefix(ct, "audio/") || media.IsAudioExt(ext):
		kind = media.KindAudio
	}
	writeJSON(w, 200, map[string]any{
		"id":       id,
		"kind":     kind,
		"filename": hdr.Filename,
		"mime":     ct,
		"size":     n,
		"path":     dst,
		"url":      "/api/uploads/" + id + ext,
	})
}

func (s *Server) apiServeUpload(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(chi.URLParam(r, "name"))
	if name == "." || name == "/" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.uploads, name)
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(s.uploads)) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *Server) apiPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text         string `json:"text"`
		UseSignature bool   `json:"use_signature"`
		HasMedia     bool   `json:"has_media"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad json"})
		return
	}
	sig := s.settings.Get(settings.KeySignature, "")
	text := format.AppendSignature(req.Text, sig, req.UseSignature)
	writeJSON(w, 200, map[string]any{
		"previews":  format.Previews(text, req.HasMedia),
		"plain_len": format.VisibleUTF16Len(text),
		"tg_limit":  map[string]any{"caption": format.TelegramCaptionLimit, "text": format.TelegramTextLimit, "has_media": req.HasMedia},
	})
}

type sendRequest struct {
	Text       string `json:"text"`
	ChannelIDs []int64 `json:"channel_ids"`
	Media      []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Path     string `json:"path"`
		Filename string `json:"filename"`
		MIME     string `json:"mime"`
		Size     int64  `json:"size"`
	} `json:"media"`
	UseSignature bool   `json:"use_signature"`
	SendAt       string `json:"send_at,omitempty"` // RFC3339 UTC, for /api/schedule
}

func (s *Server) apiSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad json"})
		return
	}
	jobID, results, err := s.executeSend(r.Context(), req, true)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"job_id": jobID, "results": results})
}

func (s *Server) apiSchedule(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad json"})
		return
	}
	if len(req.ChannelIDs) == 0 {
		writeJSON(w, 400, map[string]any{"error": "выберите хотя бы один канал"})
		return
	}
	sendAt, err := time.Parse(time.RFC3339, strings.TrimSpace(req.SendAt))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "send_at: нужен RFC3339 (UTC)"})
		return
	}
	sendAt = sendAt.UTC()
	if !sendAt.After(time.Now().UTC().Add(30 * time.Second)) {
		writeJSON(w, 400, map[string]any{"error": "время должно быть хотя бы на 30 секунд позже"})
		return
	}
	media := make([]schedule.MediaItem, 0, len(req.Media))
	for _, m := range req.Media {
		if m.Path == "" || !strings.HasPrefix(filepath.Clean(m.Path), filepath.Clean(s.uploads)) {
			writeJSON(w, 400, map[string]any{"error": "invalid media path"})
			return
		}
		media = append(media, schedule.MediaItem{
			ID: m.ID, Kind: m.Kind, Path: m.Path, Filename: m.Filename, MIME: m.MIME, Size: m.Size,
		})
	}
	textWithSig := format.AppendSignature(req.Text, s.settings.Get(settings.KeySignature, ""), req.UseSignature)
	id := uuid.NewString()
	p := schedule.Post{
		ID:           id,
		SendAt:       sendAt,
		Status:       schedule.StatusPending,
		Text:         req.Text,
		UseSignature: req.UseSignature,
		ChannelIDs:   req.ChannelIDs,
		Media:        media,
		PreviewText:  publish.TruncatePreview(format.Plain(textWithSig), 180),
	}
	if err := s.schedule.Create(p); err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"id":      id,
		"send_at": sendAt.Format(time.RFC3339),
		"status":  schedule.StatusPending,
	})
}

func (s *Server) schedulePage(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	year, month := parseYearMonth(r.URL.Query().Get("ym"), now)
	loc := now.Location()
	from := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	// Include a week before/after so edge posts in adjacent-month cells show up
	rangeFrom := from.AddDate(0, 0, -7).UTC()
	rangeTo := from.AddDate(0, 1, 7).UTC()
	posts, err := s.schedule.ListBetween(rangeFrom, rangeTo)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	cal := buildCalendar(year, month, posts, now)
	s.render(w, "schedule.html", map[string]any{
		"Title": "Расписание",
		"Cal":   cal,
		"Error": r.URL.Query().Get("e"),
		"Msg":   r.URL.Query().Get("msg"),
	})
}

func (s *Server) scheduleCancel(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := chi.URLParam(r, "id")
	ym := strings.TrimSpace(r.FormValue("ym"))
	redir := "/schedule"
	if ym != "" {
		redir += "?ym=" + url.QueryEscape(ym)
	}
	if err := s.schedule.Cancel(id); err != nil {
		sep := "?"
		if strings.Contains(redir, "?") {
			sep = "&"
		}
		http.Redirect(w, r, redir+sep+"e="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	sep := "?"
	if strings.Contains(redir, "?") {
		sep = "&"
	}
	http.Redirect(w, r, redir+sep+"msg="+url.QueryEscape("отменено"), http.StatusSeeOther)
}

// ExecuteScheduled is used by the background worker.
func (s *Server) ExecuteScheduled(ctx context.Context, p schedule.Post) (string, error) {
	req := sendRequest{
		Text:         p.Text,
		ChannelIDs:   p.ChannelIDs,
		UseSignature: p.UseSignature,
	}
	for _, m := range p.Media {
		req.Media = append(req.Media, struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			Path     string `json:"path"`
			Filename string `json:"filename"`
			MIME     string `json:"mime"`
			Size     int64  `json:"size"`
		}{ID: m.ID, Kind: m.Kind, Path: m.Path, Filename: m.Filename, MIME: m.MIME, Size: m.Size})
	}
	jobID, _, err := s.executeSend(ctx, req, false)
	return jobID, err
}

// executeSend runs the same path as immediate send. cleanupTmp removes prepared variants after send.
func (s *Server) executeSend(ctx context.Context, req sendRequest, cleanupTmp bool) (string, []publish.Result, error) {
	if len(req.ChannelIDs) == 0 {
		return "", nil, fmt.Errorf("выберите хотя бы один канал")
	}
	text := format.AppendSignature(req.Text, s.settings.Get(settings.KeySignature, ""), req.UseSignature)

	assets := make([]media.Asset, 0, len(req.Media))
	for _, m := range req.Media {
		if m.Path == "" || !strings.HasPrefix(filepath.Clean(m.Path), filepath.Clean(s.uploads)) {
			return "", nil, fmt.Errorf("invalid media path")
		}
		// Scheduled media must still exist on disk.
		if _, err := os.Stat(m.Path); err != nil {
			return "", nil, fmt.Errorf("медиафайл недоступен: %s", m.Filename)
		}
		assets = append(assets, media.Asset{
			ID: m.ID, Kind: media.Kind(m.Kind), Path: m.Path,
			Filename: m.Filename, MIME: m.MIME, Size: m.Size,
		})
	}

	post := publish.Post{TextHTML: text, Media: assets}
	jobID := uuid.NewString()
	payload, _ := json.Marshal(req)
	preview := publish.TruncatePreview(format.Plain(text), 180)
	_, _ = s.db.Exec(`INSERT INTO send_jobs(id, payload_json, status, preview_text) VALUES(?,?,?,?)`, jobID, string(payload), "running", preview)

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	results := make([]publish.Result, 0, len(req.ChannelIDs))
	for _, id := range req.ChannelIDs {
		ch, err := s.channels.Get(id)
		if err != nil {
			results = append(results, publish.Result{ChannelID: id, OK: false, Error: "channel not found"})
			continue
		}
		if !ch.Enabled {
			results = append(results, publish.Result{
				ChannelID: ch.ID, Platform: string(ch.Platform), ChannelName: ch.Name,
				OK: false, Error: "channel disabled",
			})
			continue
		}
		res := s.dispatch.Send(runCtx, ch, post)
		results = append(results, res)
		ok := 0
		if res.OK {
			ok = 1
		}
		_, _ = s.db.Exec(`INSERT INTO send_results(job_id, channel_id, platform, channel_name, ok, message_ref, post_url, error) VALUES(?,?,?,?,?,?,?,?)`,
			jobID, res.ChannelID, res.Platform, res.ChannelName, ok, res.MessageRef, res.PostURL, res.Error)
	}
	_, _ = s.db.Exec(`UPDATE send_jobs SET status=? WHERE id=?`, "done", jobID)

	if cleanupTmp {
		_ = filepath.Walk(s.tmp, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			for _, a := range assets {
				if strings.Contains(info.Name(), a.ID) {
					_ = os.Remove(path)
				}
			}
			return nil
		})
	}
	return jobID, results, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	// Keep exported field names; also accept lowercase via struct tags on Result if needed.
	_ = enc.Encode(v)
}

func platformLabel(p channels.Platform) string {
	return platformLabelStr(string(p))
}

func platformLabelStr(p string) string {
	switch channels.Platform(p) {
	case channels.PlatformTelegram:
		return "Telegram"
	case channels.PlatformDiscord:
		return "Discord"
	case channels.PlatformLolka:
		return "LOLKA"
	case channels.PlatformMAX:
		return "MAX"
	case channels.PlatformVK:
		return "VK"
	default:
		return p
	}
}

func isVideoExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp4", ".mov", ".mkv", ".webm", ".avi":
		return true
	default:
		return false
	}
}
