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
	"regexp"
	"strconv"
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

const (
	videoAudioBitrate = 96_000
	minVideoBitrate   = 120_000
	// Headroom so muxing and x264 overshoot stay under the platform cap.
	videoSizeMargin = 0.90
)

// VideoLimit is a platform whose file cap the upload exceeds.
type VideoLimit struct {
	Platform string `json:"platform"`
	Label    string `json:"label"`
	MaxBytes int64  `json:"max_bytes"`
	Over     bool   `json:"over"`
	CanFit   bool   `json:"can_fit"`
}

// VideoLimitReport lists platforms where size is over the video cap.
// durationSec 0 means duration is unknown: CanFit stays true and encode will try.
func VideoLimitReport(size int64, durationSec float64) []VideoLimit {
	order := []string{"discord", "telegram", "lolka", "max", "vk"}
	out := make([]VideoLimit, 0)
	for _, platform := range order {
		lim := DefaultLimits(platform)
		if size <= lim.MaxBytes {
			continue
		}
		canFit := true
		if durationSec > 0 {
			_, canFit = fitVideoBitrate(lim.MaxBytes, durationSec, videoAudioBitrate)
		}
		out = append(out, VideoLimit{
			Platform: platform,
			Label:    platformName(platform),
			MaxBytes: lim.MaxBytes,
			Over:     true,
			CanFit:   canFit,
		})
	}
	return out
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
	if err := os.MkdirAll(p.TmpDir, 0o755); err != nil {
		return "", err
	}
	probe, probeErr := p.probeMedia(src.Path)
	audioBps := videoAudioBitrate
	withAudio := true
	if probeErr == nil && !probe.HasAudio {
		audioBps = 0
		withAudio = false
	}
	bps := parseBitrate(lim.VideoBitrate)
	if probeErr == nil && probe.Duration > 0 {
		fit, ok := fitVideoBitrate(lim.MaxBytes, probe.Duration, audioBps)
		if !ok {
			return "", videoLimitError(platform, lim.MaxBytes)
		}
		if fit < bps {
			bps = fit
		}
	}
	out := filepath.Join(p.TmpDir, src.ID+"_"+platform+".mp4")
	for attempt := 0; attempt < 3; attempt++ {
		if err := p.transcodeVideo(src.Path, out, videoWidth(lim.VideoMaxW, bps), bps, withAudio); err != nil {
			return "", err
		}
		st, err := os.Stat(out)
		if err != nil {
			return "", err
		}
		if st.Size() <= lim.MaxBytes {
			return out, nil
		}
		next := int(float64(bps) * float64(lim.MaxBytes) / float64(st.Size()) * videoSizeMargin)
		if next >= bps {
			next = bps * 3 / 4
		}
		if next < minVideoBitrate {
			break
		}
		bps = next
	}
	return "", videoLimitError(platform, lim.MaxBytes)
}

func (p *Processor) transcodeVideo(src, out string, width, videoBps int, withAudio bool) error {
	bin := "ffmpeg"
	if p != nil && p.FFmpeg != "" {
		bin = p.FFmpeg
	}
	args := []string{
		"-y", "-i", src,
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", width),
		"-c:v", "libx264", "-preset", "veryfast",
		"-b:v", strconv.Itoa(videoBps),
		"-maxrate", strconv.Itoa(videoBps),
		"-bufsize", strconv.Itoa(videoBps * 2),
		"-movflags", "+faststart",
	}
	if withAudio {
		args = append(args, "-c:a", "aac", "-b:a", "96k")
	} else {
		args = append(args, "-an")
	}
	args = append(args, out)
	cmd := exec.Command(bin, args...)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, truncate(string(stderr), 400))
	}
	return nil
}

func (p *Processor) ProbeDuration(path string) (float64, error) {
	info, err := p.probeMedia(path)
	return info.Duration, err
}

func (p *Processor) probeMedia(path string) (mediaProbe, error) {
	bin := "ffmpeg"
	if p != nil && p.FFmpeg != "" {
		bin = p.FFmpeg
	}
	cmd := exec.Command(bin, "-hide_banner", "-i", path)
	out, _ := cmd.CombinedOutput()
	return parseFFMPEGProbe(string(out))
}

type mediaProbe struct {
	Duration float64
	HasAudio bool
}

var durationRe = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

func parseFFMPEGProbe(out string) (mediaProbe, error) {
	m := durationRe.FindStringSubmatch(out)
	if m == nil {
		return mediaProbe{}, fmt.Errorf("duration not found")
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	sec, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return mediaProbe{}, err
	}
	return mediaProbe{
		Duration: float64(h)*3600 + float64(min)*60 + sec,
		HasAudio: strings.Contains(out, "Audio:"),
	}, nil
}

// fitVideoBitrate picks a video bitrate that should land under maxBytes.
// ok is false when even the minimum watchable bitrate cannot fit.
func fitVideoBitrate(maxBytes int64, durationSec float64, audioBps int) (int, bool) {
	if durationSec < 0.1 || maxBytes <= 0 {
		return 0, false
	}
	budget := float64(maxBytes) * 8 * videoSizeMargin
	videoBits := budget - float64(audioBps)*durationSec
	bps := int(videoBits / durationSec)
	if bps < minVideoBitrate {
		return bps, false
	}
	return bps, true
}

func videoWidth(maxW, bps int) int {
	if maxW <= 0 {
		maxW = 1280
	}
	w := maxW
	switch {
	case bps < 280_000:
		w = 480
	case bps < 550_000:
		w = 640
	case bps < 900_000:
		w = 854
	case bps < 1_400_000:
		w = 960
	}
	if w > maxW {
		return maxW
	}
	return w
}

func parseBitrate(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 2_000_000
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult = 1000
		s = strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult = 1_000_000
		s = strings.TrimSuffix(s, "m")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 2_000_000
	}
	return int(f * mult)
}

func videoLimitError(platform string, maxBytes int64) error {
	return fmt.Errorf("%s: видео больше лимита %s даже после сжатия. Укоротите ролик", platformName(platform), formatBytesMB(maxBytes))
}

func platformName(platform string) string {
	switch platform {
	case "telegram":
		return "Telegram"
	case "discord":
		return "Discord"
	case "lolka":
		return "LOLKA"
	case "vk":
		return "VK"
	case "max":
		return "MAX"
	default:
		return platform
	}
}

func formatBytesMB(n int64) string {
	const mb = 1024 * 1024
	if n > 0 && n%mb == 0 {
		return fmt.Sprintf("%d МБ", n/mb)
	}
	return fmt.Sprintf("%.1f МБ", float64(n)/float64(mb))
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
