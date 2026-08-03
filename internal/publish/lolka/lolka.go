package lolka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"newsmaker/internal/channels"
	"newsmaker/internal/format"
	"newsmaker/internal/publish"
)

// snowflake accepts IDs as JSON strings or numbers.
type snowflake string

func (s *snowflake) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*s = ""
		return nil
	}
	if b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = snowflake(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = snowflake(n.String())
	return nil
}

type Publisher struct {
	Client *http.Client
}

func (p *Publisher) Platform() channels.Platform { return channels.PlatformLolka }

func (p *Publisher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Publisher) Publish(ctx context.Context, ch channels.Channel, post publish.Post, prepared []string) (string, error) {
	webhook := strings.TrimSpace(ch.Credential)
	if webhook == "" {
		return "", fmt.Errorf("lolka: empty webhook url")
	}
	content := format.ForLolka(post.TextHTML)
	if len(content) > 2000 {
		content = content[:1997] + "..."
	}

	endpoint := withWait(webhook)

	if len(prepared) == 0 {
		payload, _ := json.Marshal(map[string]string{"content": content})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.client().Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		return parseWebhook(resp)
	}

	// Lolka multipart: form fields (content, …) + files[n] — not Discord's payload_json.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("content", content); err != nil {
		return "", err
	}
	for i, path := range prepared {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		part, err := w.CreateFormFile(fmt.Sprintf("files[%d]", i), filepath.Base(path))
		if err != nil {
			f.Close()
			return "", err
		}
		_, err = io.Copy(part, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return parseWebhook(resp)
}

func withWait(webhook string) string {
	u, err := url.Parse(webhook)
	if err != nil {
		if strings.Contains(webhook, "?") {
			return webhook + "&wait=true"
		}
		return webhook + "?wait=true"
	}
	q := u.Query()
	q.Set("wait", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

func parseWebhook(resp *http.Response) (string, error) {
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("lolka webhook %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		ID        snowflake `json:"id"`
		ChannelID snowflake `json:"channel_id"`
		ServerID  snowflake `json:"server_id"`
		GuildID   snowflake `json:"guild_id"`
	}
	_ = json.Unmarshal(body, &parsed)
	id := strings.TrimSpace(string(parsed.ID))
	if id == "" {
		return "ok", nil
	}
	server := strings.TrimSpace(string(parsed.ServerID))
	if server == "" {
		server = strings.TrimSpace(string(parsed.GuildID))
	}
	channel := strings.TrimSpace(string(parsed.ChannelID))
	if server != "" && channel != "" {
		if _, err := strconv.ParseUint(id, 10, 64); err == nil {
			return fmt.Sprintf("https://lolka.app/servers/%s/channels/%s/%s", server, channel, id), nil
		}
	}
	return id, nil
}
