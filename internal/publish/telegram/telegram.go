package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"newsmaker/internal/channels"
	"newsmaker/internal/format"
	"newsmaker/internal/publish"
)

type Publisher struct {
	Client *http.Client
}

func (p *Publisher) Platform() channels.Platform { return channels.PlatformTelegram }

func (p *Publisher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Publisher) Publish(ctx context.Context, ch channels.Channel, post publish.Post, prepared []string) (string, error) {
	token := strings.TrimSpace(ch.Credential)
	chatID := strings.TrimSpace(ch.TargetID)
	hasMedia := len(prepared) > 0
	text, _ := format.FitTelegram(post.TextHTML, hasMedia)

	if len(prepared) == 0 {
		return p.api(ctx, token, "sendMessage", map[string]string{
			"chat_id":    chatID,
			"text":       text,
			"parse_mode": "HTML",
		})
	}
	if len(prepared) == 1 {
		kind := guessKind(prepared[0])
		method, field := "sendPhoto", "photo"
		switch kind {
		case "video":
			method, field = "sendVideo", "video"
		case "audio":
			method, field = "sendAudio", "audio"
		}
		return p.sendFile(ctx, token, method, chatID, field, prepared[0], text)
	}
	// album: photos only for simplicity; mixed falls back to sequential
	allPhotos := true
	for _, path := range prepared {
		if guessKind(path) != "photo" {
			allPhotos = false
			break
		}
	}
	if allPhotos {
		return p.sendAlbum(ctx, token, chatID, prepared, text)
	}
	var last string
	for i, path := range prepared {
		caption := ""
		if i == 0 {
			caption = text
		}
		kind := guessKind(path)
		method, field := "sendPhoto", "photo"
		switch kind {
		case "video":
			method, field = "sendVideo", "video"
		case "audio":
			method, field = "sendAudio", "audio"
		}
		ref, err := p.sendFile(ctx, token, method, chatID, field, path, caption)
		if err != nil {
			return last, err
		}
		last = ref
	}
	return last, nil
}

func (p *Publisher) sendFile(ctx context.Context, token, method, chatID, field, path, caption string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", chatID)
	_ = w.WriteField("parse_mode", "HTML")
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	part, err := w.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	_ = w.Close()
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parseTG(resp)
}

func (p *Publisher) sendAlbum(ctx context.Context, token, chatID string, paths []string, caption string) (string, error) {
	type mediaItem struct {
		Type    string `json:"type"`
		Media   string `json:"media"`
		Caption string `json:"caption,omitempty"`
		Parse   string `json:"parse_mode,omitempty"`
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("chat_id", chatID)
	items := make([]mediaItem, 0, len(paths))
	for i, path := range paths {
		name := fmt.Sprintf("file%d", i)
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		part, err := w.CreateFormFile(name, filepath.Base(path))
		if err != nil {
			f.Close()
			return "", err
		}
		_, err = io.Copy(part, f)
		f.Close()
		if err != nil {
			return "", err
		}
		it := mediaItem{Type: "photo", Media: "attach://" + name}
		if i == 0 && caption != "" {
			it.Caption = caption
			it.Parse = "HTML"
		}
		items = append(items, it)
	}
	raw, _ := json.Marshal(items)
	_ = w.WriteField("media", string(raw))
	_ = w.Close()
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMediaGroup", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parseTG(resp)
}

func (p *Publisher) api(ctx context.Context, token, method string, fields map[string]string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parseTG(resp)
}

func parseTG(resp *http.Response) (string, error) {
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Desc   string          `json:"description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("telegram: bad json: %s", string(body))
	}
	if !parsed.OK {
		return "", fmt.Errorf("telegram: %s", parsed.Desc)
	}
	var one struct {
		MessageID int64 `json:"message_id"`
	}
	if json.Unmarshal(parsed.Result, &one) == nil && one.MessageID != 0 {
		return strconv.FormatInt(one.MessageID, 10), nil
	}
	return "ok", nil
}

func guessKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".mov", ".mkv", ".webm":
		return "video"
	case ".mp3", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav", ".flac":
		return "audio"
	default:
		return "photo"
	}
}
