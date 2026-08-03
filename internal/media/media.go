package media

import (
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Kind string

const (
	KindPhoto Kind = "photo"
	KindVideo Kind = "video"
	KindAudio Kind = "audio"
)

type Asset struct {
	ID       string
	Kind     Kind
	Path     string
	Filename string
	MIME     string
	Size     int64
}

type Limits struct {
	MaxBytes      int64
	MaxLongEdge   int
	JPEGQuality   int
	VideoMaxW     int
	VideoBitrate  string
	AudioMaxBytes int64
}

func DefaultLimits(platform string) Limits {
	switch platform {
	case "telegram":
		return Limits{MaxBytes: 10 << 20, MaxLongEdge: 2560, JPEGQuality: 85, VideoMaxW: 1280, VideoBitrate: "2500k", AudioMaxBytes: 50 << 20}
	case "discord":
		return Limits{MaxBytes: 8 << 20, MaxLongEdge: 1920, JPEGQuality: 85, VideoMaxW: 1280, VideoBitrate: "2000k", AudioMaxBytes: 8 << 20}
	case "lolka":
		// Base upload limit 10 MB (boost can raise to 50/100 MB).
		return Limits{MaxBytes: 10 << 20, MaxLongEdge: 1920, JPEGQuality: 85, VideoMaxW: 1280, VideoBitrate: "2000k", AudioMaxBytes: 10 << 20}
	case "vk":
		return Limits{MaxBytes: 50 << 20, MaxLongEdge: 2560, JPEGQuality: 85, VideoMaxW: 1280, VideoBitrate: "2500k", AudioMaxBytes: 200 << 20}
	case "max":
		return Limits{MaxBytes: 20 << 20, MaxLongEdge: 2560, JPEGQuality: 85, VideoMaxW: 1280, VideoBitrate: "2500k", AudioMaxBytes: 20 << 20}
	default:
		return Limits{MaxBytes: 10 << 20, MaxLongEdge: 1920, JPEGQuality: 85, VideoMaxW: 1280, VideoBitrate: "2000k", AudioMaxBytes: 20 << 20}
	}
}

type Processor struct {
	FFmpeg string
	TmpDir string
}

func (p *Processor) PreparePhoto(src Asset, platform string) (string, error) {
	lim := DefaultLimits(platform)
	f, err := os.Open(src.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}
	img = resizeLongEdge(img, lim.MaxLongEdge)
	out := filepath.Join(p.TmpDir, src.ID+"_"+platform+".jpg")
	if err := os.MkdirAll(p.TmpDir, 0o755); err != nil {
		return "", err
	}
	of, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer of.Close()
	if err := jpeg.Encode(of, img, &jpeg.Options{Quality: lim.JPEGQuality}); err != nil {
		return "", err
	}
	info, err := of.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > lim.MaxBytes {
		return "", fmt.Errorf("%s photo exceeds %d bytes after convert", platform, lim.MaxBytes)
	}
	return out, nil
}

func (p *Processor) PrepareVideo(src Asset, platform string) (string, error) {
	lim := DefaultLimits(platform)
	info, err := os.Stat(src.Path)
	if err != nil {
		return "", err
	}
	if info.Size() <= lim.MaxBytes && looksCompatibleVideo(src.MIME, src.Filename) {
		return src.Path, nil
	}
	if p.FFmpeg == "" {
		p.FFmpeg = "ffmpeg"
	}
	if err := os.MkdirAll(p.TmpDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(p.TmpDir, src.ID+"_"+platform+".mp4")
	args := []string{
		"-y", "-i", src.Path,
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", lim.VideoMaxW),
		"-c:v", "libx264", "-preset", "veryfast", "-b:v", lim.VideoBitrate,
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		out,
	}
	cmd := exec.Command(p.FFmpeg, args...)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg: %w: %s", err, truncate(string(stderr), 400))
	}
	st, err := os.Stat(out)
	if err != nil {
		return "", err
	}
	if st.Size() > lim.MaxBytes {
		return "", fmt.Errorf("%s video still exceeds %d bytes after compress", platform, lim.MaxBytes)
	}
	return out, nil
}

func resizeLongEdge(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > w {
		long = h
	}
	if maxEdge <= 0 || long <= maxEdge {
		return img
	}
	scale := float64(maxEdge) / float64(long)
	nw := int(float64(w) * scale)
	nh := int(float64(h) * scale)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

func (p *Processor) PrepareAudio(src Asset, platform string) (string, error) {
	lim := DefaultLimits(platform)
	info, err := os.Stat(src.Path)
	if err != nil {
		return "", err
	}
	if info.Size() <= lim.AudioMaxBytes && looksCompatibleAudio(src.MIME, src.Filename) {
		return src.Path, nil
	}
	if p.FFmpeg == "" {
		p.FFmpeg = "ffmpeg"
	}
	if err := os.MkdirAll(p.TmpDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(p.TmpDir, src.ID+"_"+platform+".mp3")
	args := []string{
		"-y", "-i", src.Path,
		"-vn",
		"-c:a", "libmp3lame", "-b:a", "192k",
		out,
	}
	cmd := exec.Command(p.FFmpeg, args...)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg audio: %w: %s", err, truncate(string(stderr), 400))
	}
	st, err := os.Stat(out)
	if err != nil {
		return "", err
	}
	if st.Size() > lim.AudioMaxBytes {
		return "", fmt.Errorf("%s audio still exceeds %d bytes after compress", platform, lim.AudioMaxBytes)
	}
	return out, nil
}

func looksCompatibleAudio(mime, name string) bool {
	mime = strings.ToLower(mime)
	name = strings.ToLower(name)
	if strings.HasPrefix(mime, "audio/") {
		return strings.Contains(mime, "mpeg") || strings.Contains(mime, "mp3") || strings.Contains(mime, "mp4") || strings.Contains(mime, "m4a") || strings.Contains(mime, "aac")
	}
	switch filepath.Ext(name) {
	case ".mp3", ".m4a", ".aac":
		return true
	default:
		return false
	}
}

func IsAudioExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp3", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".wav", ".flac", ".wma":
		return true
	default:
		return false
	}
}

func looksCompatibleVideo(mime, name string) bool {
	mime = strings.ToLower(mime)
	name = strings.ToLower(name)
	return strings.Contains(mime, "mp4") || strings.HasSuffix(name, ".mp4") || strings.HasSuffix(name, ".mov")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func CopyUpload(src io.Reader, dstPath string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(dstPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, src)
}
