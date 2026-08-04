package vk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"newsmaker/internal/channels"
	"newsmaker/internal/format"
	"newsmaker/internal/publish"
)

type Publisher struct {
	Client  *http.Client
	APIVers string
}

func (p *Publisher) Platform() channels.Platform { return channels.PlatformVK }

func (p *Publisher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
}

func (p *Publisher) ver() string {
	if p.APIVers != "" {
		return p.APIVers
	}
	return "5.199"
}

func (p *Publisher) Publish(ctx context.Context, ch channels.Channel, post publish.Post, prepared []string) (string, error) {
	token := strings.TrimSpace(ch.Credential)
	owner := strings.TrimSpace(ch.TargetID)
	text := format.ForVK(post.TextHTML)

	var attachments []string
	for _, path := range prepared {
		ext := strings.ToLower(filepath.Ext(path))
		var att string
		var err error
		switch {
		case ext == ".mp4" || ext == ".mov" || ext == ".webm":
			att, err = p.uploadVideo(ctx, token, owner, path, text)
		case isAudioExt(ext):
			att, err = p.uploadDoc(ctx, token, owner, path)
		default:
			att, err = p.uploadPhoto(ctx, token, owner, path)
		}
		if err != nil {
			return "", err
		}
		attachments = append(attachments, att)
	}

	form := url.Values{}
	form.Set("access_token", token)
	form.Set("v", p.ver())
	form.Set("owner_id", owner)
	form.Set("from_group", "1")
	form.Set("message", text)
	form.Set("primary_attachments_mode", "grid")
	if len(attachments) > 0 {
		form.Set("attachments", strings.Join(attachments, ","))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vk.com/method/wall.post", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Response struct {
			PostID int64 `json:"post_id"`
		} `json:"response"`
		Error *struct {
			Code    int    `json:"error_code"`
			Message string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("vk wall.post: %s", string(body))
	}
	if parsed.Error != nil {
		msg := parsed.Error.Message
		if strings.Contains(strings.ToLower(msg), "group auth") {
			return "", fmt.Errorf("vk: %s — нужен user-токен админа группы (не ключ сообщества), owner_id=-group_id", msg)
		}
		return "", fmt.Errorf("vk: %s", msg)
	}
	return strconv.FormatInt(parsed.Response.PostID, 10), nil
}

func (p *Publisher) uploadPhoto(ctx context.Context, token, owner, path string) (string, error) {
	gid := strings.TrimPrefix(owner, "-")
	form := url.Values{}
	form.Set("access_token", token)
	form.Set("v", p.ver())
	form.Set("group_id", gid)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vk.com/method/photos.getWallUploadServer", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var server struct {
		Response struct {
			UploadURL string `json:"upload_url"`
		} `json:"response"`
		Error *struct {
			Message string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &server); err != nil || server.Error != nil || server.Response.UploadURL == "" {
		msg := string(body)
		if server.Error != nil {
			msg = server.Error.Message
		}
		return "", fmt.Errorf("vk getWallUploadServer: %s", msg)
	}

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(fileBytes) == 0 {
		return "", fmt.Errorf("vk upload photo: empty file %s", path)
	}
	filename := "photo.jpg"
	if ext := strings.ToLower(filepath.Ext(path)); hasImageExt(path) {
		filename = "photo" + ext
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="photo"; filename="%s"`, filename))
	h.Set("Content-Type", "image/jpeg")
	if strings.HasSuffix(filename, ".png") {
		h.Set("Content-Type", "image/png")
	}
	part, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	// VK upload hosts often reject chunked bodies — send with known Content-Length.
	bodyReader := bytes.NewReader(buf.Bytes())
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, server.Response.UploadURL, bodyReader)
	if err != nil {
		return "", err
	}
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upReq.ContentLength = int64(buf.Len())
	upResp, err := p.client().Do(upReq)
	if err != nil {
		return "", err
	}
	defer upResp.Body.Close()
	upBody, _ := io.ReadAll(upResp.Body)

	// VK returns photo as a JSON *string* (contents like `[{...}]`), not a raw array.
	var uploaded struct {
		Server int    `json:"server"`
		Photo  string `json:"photo"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(upBody, &uploaded); err != nil {
		return "", fmt.Errorf("vk upload photo: %s", string(upBody))
	}
	photoParam := strings.TrimSpace(uploaded.Photo)
	if photoParam == "" || photoParam == "[]" || photoParam == "null" {
		return "", fmt.Errorf("vk upload photo empty (need .jpg/.png filename): %s", string(upBody))
	}

	save := url.Values{}
	save.Set("access_token", token)
	save.Set("v", p.ver())
	save.Set("group_id", gid)
	save.Set("server", strconv.Itoa(uploaded.Server))
	save.Set("photo", photoParam)
	save.Set("hash", uploaded.Hash)
	saveReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vk.com/method/photos.saveWallPhoto", strings.NewReader(save.Encode()))
	if err != nil {
		return "", err
	}
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveResp, err := p.client().Do(saveReq)
	if err != nil {
		return "", err
	}
	defer saveResp.Body.Close()
	saveBody, _ := io.ReadAll(saveResp.Body)
	var saved struct {
		Response []struct {
			ID      int64 `json:"id"`
			OwnerID int64 `json:"owner_id"`
		} `json:"response"`
		Error *struct {
			Message string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(saveBody, &saved); err != nil || saved.Error != nil || len(saved.Response) == 0 {
		msg := string(saveBody)
		if saved.Error != nil {
			msg = saved.Error.Message
		}
		return "", fmt.Errorf("vk saveWallPhoto: %s", msg)
	}
	ph := saved.Response[0]
	return fmt.Sprintf("photo%d_%d", ph.OwnerID, ph.ID), nil
}

func hasImageExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func (p *Publisher) uploadVideo(ctx context.Context, token, owner, path, name string) (string, error) {
	gid := strings.TrimPrefix(owner, "-")
	form := url.Values{}
	form.Set("access_token", token)
	form.Set("v", p.ver())
	form.Set("group_id", gid)
	form.Set("name", truncate(name, 80))
	form.Set("wallpost", "0")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vk.com/method/video.save", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Response struct {
			UploadURL string `json:"upload_url"`
			VideoID   int64  `json:"video_id"`
			OwnerID   int64  `json:"owner_id"`
		} `json:"response"`
		Error *struct {
			Message string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error != nil || parsed.Response.UploadURL == "" {
		msg := string(body)
		if parsed.Error != nil {
			msg = parsed.Error.Message
		}
		return "", fmt.Errorf("vk video.save: %s", msg)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	part, err := w.CreateFormFile("video_file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	_ = w.Close()
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.Response.UploadURL, &buf)
	if err != nil {
		return "", err
	}
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upResp, err := p.client().Do(upReq)
	if err != nil {
		return "", err
	}
	defer upResp.Body.Close()
	_, _ = io.ReadAll(upResp.Body)
	return fmt.Sprintf("video%d_%d", parsed.Response.OwnerID, parsed.Response.VideoID), nil
}

func (p *Publisher) uploadDoc(ctx context.Context, token, owner, path string) (string, error) {
	gid := strings.TrimPrefix(owner, "-")
	form := url.Values{}
	form.Set("access_token", token)
	form.Set("v", p.ver())
	form.Set("group_id", gid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vk.com/method/docs.getWallUploadServer", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var server struct {
		Response struct {
			UploadURL string `json:"upload_url"`
		} `json:"response"`
		Error *struct {
			Message string `json:"error_msg"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &server); err != nil || server.Error != nil || server.Response.UploadURL == "" {
		msg := string(body)
		if server.Error != nil {
			msg = server.Error.Message
		}
		low := strings.ToLower(msg)
		if strings.Contains(low, "access denied") || strings.Contains(low, "scopes") {
			return "", fmt.Errorf("vk docs.getWallUploadServer: %s — для аудио в VK нужен user-токен со scope docs (перевыпусти токен с photos,offline,groups,docs)", msg)
		}
		return "", fmt.Errorf("vk docs.getWallUploadServer: %s", msg)
	}

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return "", err
	}
	_ = w.Close()
	bodyReader := bytes.NewReader(buf.Bytes())
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, server.Response.UploadURL, bodyReader)
	if err != nil {
		return "", err
	}
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upReq.ContentLength = int64(buf.Len())
	upResp, err := p.client().Do(upReq)
	if err != nil {
		return "", err
	}
	defer upResp.Body.Close()
	upBody, _ := io.ReadAll(upResp.Body)
	var uploaded struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(upBody, &uploaded); err != nil || uploaded.File == "" {
		return "", fmt.Errorf("vk docs upload: %s", string(upBody))
	}

	save := url.Values{}
	save.Set("access_token", token)
	save.Set("v", p.ver())
	save.Set("file", uploaded.File)
	save.Set("title", filepath.Base(path))
	saveReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.vk.com/method/docs.save", strings.NewReader(save.Encode()))
	if err != nil {
		return "", err
	}
	saveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveResp, err := p.client().Do(saveReq)
	if err != nil {
		return "", err
	}
	defer saveResp.Body.Close()
	saveBody, _ := io.ReadAll(saveResp.Body)
	var saved struct {
		Response struct {
			Doc struct {
				ID      int64 `json:"id"`
				OwnerID int64 `json:"owner_id"`
			} `json:"doc"`
			Type string `json:"type"`
		} `json:"response"`
		Error *struct {
			Message string `json:"error_msg"`
		} `json:"error"`
	}
	// docs.save may return {response: {type:"doc", doc:{...}}} or array in older versions
	if err := json.Unmarshal(saveBody, &saved); err != nil || saved.Error != nil || saved.Response.Doc.ID == 0 {
		// try array form
		var alt struct {
			Response []struct {
				ID      int64 `json:"id"`
				OwnerID int64 `json:"owner_id"`
			} `json:"response"`
			Error *struct {
				Message string `json:"error_msg"`
			} `json:"error"`
		}
		if json.Unmarshal(saveBody, &alt) == nil && alt.Error == nil && len(alt.Response) > 0 {
			d := alt.Response[0]
			return fmt.Sprintf("doc%d_%d", d.OwnerID, d.ID), nil
		}
		msg := string(saveBody)
		if saved.Error != nil {
			msg = saved.Error.Message
		}
		return "", fmt.Errorf("vk docs.save: %s", msg)
	}
	return fmt.Sprintf("doc%d_%d", saved.Response.Doc.OwnerID, saved.Response.Doc.ID), nil
}

func isAudioExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp3", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav", ".flac", ".wma":
		return true
	default:
		return false
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "video"
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
