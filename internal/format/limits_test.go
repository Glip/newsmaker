package format

import (
	"strings"
	"testing"
)

func TestVisibleUTF16LenIgnoresHref(t *testing.T) {
	s := `hello <a href="https://example.com/very/long/path">world</a>!`
	if n := VisibleUTF16Len(s); n != 12 {
		t.Fatalf("VisibleUTF16Len=%d want 12", n)
	}
	if n := PlainUTF16Len(s); n <= 12 {
		t.Fatalf("PlainUTF16Len=%d should include URL and be > 12", n)
	}
}

func TestTruncateCanonicalCaption(t *testing.T) {
	text := strings.Repeat("а", 1200)
	cut, trunc := TruncateCanonical(text, TelegramCaptionLimit)
	if !trunc {
		t.Fatal("expected truncation")
	}
	if n := VisibleUTF16Len(cut); n > TelegramCaptionLimit {
		t.Fatalf("truncated len=%d > %d", n, TelegramCaptionLimit)
	}
	if !strings.HasSuffix(cut, "…") {
		t.Fatalf("expected ellipsis, got tail %q", cut[len(cut)-8:])
	}
}

func TestTruncateKeepsLinkBeforeCut(t *testing.T) {
	s := strings.Repeat("x", 1000) + `<a href="https://example.com/page">link</a>` + strings.Repeat("y", 50)
	cut, trunc := TruncateCanonical(s, TelegramCaptionLimit)
	if !trunc {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(cut, `<a href="https://example.com/page">`) {
		t.Fatalf("link opening tag lost near cut: …%q", cut[len(cut)-80:])
	}
	if VisibleUTF16Len(cut) > TelegramCaptionLimit {
		t.Fatalf("over limit: %d", VisibleUTF16Len(cut))
	}
}

func TestFitTelegramMedia(t *testing.T) {
	text := strings.Repeat("б", 2000)
	out, trunc := FitTelegram(text, true)
	if !trunc {
		t.Fatal("expected trunc")
	}
	if VisibleUTF16Len(out) > TelegramCaptionLimit {
		t.Fatalf("len=%d", VisibleUTF16Len(out))
	}
}

func TestPreviewsTelegramTruncatesWithMedia(t *testing.T) {
	text := strings.Repeat("в", 1500)
	previews := Previews(text, true)
	var tg *PlatformPreview
	for i := range previews {
		if previews[i].Platform == "telegram" {
			tg = &previews[i]
			break
		}
	}
	if tg == nil {
		t.Fatal("no telegram preview")
	}
	if tg.Note == "" {
		t.Fatal("expected truncation note")
	}
	if !strings.Contains(tg.Note, "1024") {
		t.Fatalf("note=%q", tg.Note)
	}
	// preview HTML should not contain the full 1500 letters
	plain := strings.ReplaceAll(tg.HTML, "<br>", "")
	// strip tags roughly
	for {
		start := strings.Index(plain, "<")
		if start < 0 {
			break
		}
		end := strings.Index(plain[start:], ">")
		if end < 0 {
			break
		}
		plain = plain[:start] + plain[start+end+1:]
	}
	if strings.Count(plain, "в") >= 1500 {
		t.Fatalf("preview not truncated, count=%d", strings.Count(plain, "в"))
	}
}
