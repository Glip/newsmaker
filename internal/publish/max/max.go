package maxbot

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
	"strings"
	"time"

	"newsmaker/internal/channels"
	"newsmaker/internal/format"
	"newsmaker/internal/publish"
)

// Publisher talks to MAX Bot API.
type Publisher struct {
	Client  *http.Client
	BaseURL string
}

func (p *Publisher) Platform() channels.Platform { return channels.PlatformMAX }

func (p *Publisher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Publisher) base() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	// Docs recommend platform-api2.max.ru for bots/mini-apps.
	return "https://platform-api2.max.ru"
}

func (p *Publisher) Publish(ctx context.Context, ch channels.Channel, post publish.Post, prepared []string) (string, error) {
	token := strings.TrimSpace(ch.Credential)
	chatID := strings.TrimSpace(ch.TargetID)
	text := format.ForMAX(post.TextHTML)

	attachments := make([]map[string]any, 0, len(prepared))
	for _, path := range prepared {
		att, err := p.upload(ctx, token, path)
		if err != nil {
			return "", err
		}
		attachments = append(attachments, att)
	}

	body := map[string]any{
		"text":   text,
		"format": "html",
	}
	if len(attachments) > 0 {
		body["attachments"] = attachments
	}

	var lastErr error
	delay := time.Second
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
		ref, err := p.sendMessage(ctx, token, chatID, body)
		if err == nil {
			return ref, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "attachment.not.ready") && !strings.Contains(err.Error(), "not.processed") {
			return "", err
		}
	}
	return "", lastErr
}

func (p *Publisher) sendMessage(ctx context.Context, token, chatID string, body map[string]any) (string, error) {
	raw, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/messages?chat_id=%s", p.base(), chatID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("max %d: %s", resp.StatusCode, string(respBody))
	}
	var parsed struct {
		Message struct {
			Body struct {
				Mid string `json:"mid"`
			} `json:"body"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &parsed)
	if parsed.Message.Body.Mid != "" {
		return parsed.Message.Body.Mid, nil
	}
	return "ok", nil
}

func (p *Publisher) upload(ctx context.Context, botToken, path string) (map[string]any, error) {
	kind := "image"
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".mov", ".webm", ".mkv":
		kind = "video"
	case ".mp3", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav", ".flac":
		kind = "audio"
	}

	initURL := fmt.Sprintf("%s/uploads?type=%s", p.base(), kind)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, initURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", botToken)
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	initBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("max upload init %d: %s", resp.StatusCode, string(initBody))
	}
	var init struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(initBody, &init); err != nil || init.URL == "" {
		return nil, fmt.Errorf("max upload init: bad response %s", string(initBody))
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	part, err := w.CreateFormFile("data", filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	_ = w.Close()

	bodyReader := bytes.NewReader(buf.Bytes())
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, init.URL, bodyReader)
	if err != nil {
		return nil, err
	}
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upReq.ContentLength = int64(buf.Len())
	// Some MAX upload hosts also expect Authorization.
	upReq.Header.Set("Authorization", botToken)
	upResp, err := p.client().Do(upReq)
	if err != nil {
		return nil, err
	}
	defer upResp.Body.Close()
	upBody, _ := io.ReadAll(upResp.Body)
	if upResp.StatusCode >= 300 {
		return nil, fmt.Errorf("max upload %d: %s", upResp.StatusCode, string(upBody))
	}

	// For video/audio: token comes from /uploads init; upload body is often just "1" / {"retval":1}.
	tokenVal := init.Token
	if tokenVal == "" {
		tokenVal = extractUploadToken(upBody)
	}
	if tokenVal == "" {
		return nil, fmt.Errorf("max upload: no token (init=%s upload=%s)", string(initBody), string(upBody))
	}

	typ := "image"
	switch kind {
	case "video":
		typ = "video"
	case "audio":
		typ = "audio"
	}
	return map[string]any{
		"type": typ,
		"payload": map[string]any{
			"token": tokenVal,
		},
	}, nil
}

func extractUploadToken(upBody []byte) string {
	trimmed := strings.TrimSpace(string(upBody))
	if trimmed == "" || trimmed == "1" || trimmed == "true" {
		return ""
	}
	var uploaded map[string]any
	if err := json.Unmarshal(upBody, &uploaded); err != nil {
		return ""
	}
	if t, ok := uploaded["token"].(string); ok && t != "" {
		return t
	}
	if m, ok := uploaded["photos"].(map[string]any); ok {
		for _, v := range m {
			if pm, ok := v.(map[string]any); ok {
				if t, ok := pm["token"].(string); ok && t != "" {
					return t
				}
			}
		}
	}
	return ""
}
