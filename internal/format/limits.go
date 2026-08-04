package format

import (
	"html"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Platform text limits (Telegram: after entities parsing ≈ visible UTF-16 length).
const (
	TelegramTextLimit    = 4096
	TelegramCaptionLimit = 1024
	DiscordContentLimit  = 2000
	LolkaContentLimit    = 2000
)

// UTF16Len returns the length Telegram/Discord use for limits (UTF-16 code units).
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		w := utf16.RuneLen(r)
		if w < 1 {
			w = 2
		}
		n += w
	}
	return n
}

// VisibleText is plain text as Telegram counts it after entities parsing:
// markup tags removed, <a> contributes only the label (not the URL).
func VisibleText(canonical string) string {
	s := reAnchor.ReplaceAllString(canonical, "$2")
	s = stripRemainingTags(s)
	return html.UnescapeString(s)
}

// VisibleUTF16Len is the Telegram entity length of canonical markup.
func VisibleUTF16Len(canonical string) int {
	return UTF16Len(VisibleText(canonical))
}

// PlainUTF16Len is kept for history previews (includes URLs from anchors).
func PlainUTF16Len(canonical string) int {
	return UTF16Len(Plain(canonical))
}

// TruncateUTF16 truncates s to at most max UTF-16 code units, adding "…" if cut.
func TruncateUTF16(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if UTF16Len(s) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	var b strings.Builder
	n := 0
	limit := max - 1 // room for ellipsis
	for _, r := range s {
		w := utf16.RuneLen(r)
		if w < 1 {
			w = 2
		}
		if n+w > limit {
			b.WriteRune('…')
			return b.String()
		}
		b.WriteRune(r)
		n += w
	}
	return b.String()
}

// TruncateCanonical cuts canonical markup so visible text fits max UTF-16 units.
func TruncateCanonical(canonical string, max int) (string, bool) {
	canonical = strings.TrimSpace(canonical)
	if max <= 0 {
		return "", canonical != ""
	}
	if VisibleUTF16Len(canonical) <= max {
		return canonical, false
	}
	return truncateCanonicalKeepTags(canonical, max), true
}

// FitTelegram returns Telegram HTML within text/caption limits.
func FitTelegram(canonical string, hasMedia bool) (htmlOut string, truncated bool) {
	max := TelegramTextLimit
	if hasMedia {
		max = TelegramCaptionLimit
	}
	cut, trunc := TruncateCanonical(canonical, max)
	return ForTelegramHTML(cut), trunc
}

// FitDiscord returns Discord markdown within the content limit.
func FitDiscord(canonical string) (md string, truncated bool) {
	md = ForDiscord(canonical)
	if UTF16Len(md) <= DiscordContentLimit {
		return md, false
	}
	return TruncateUTF16(md, DiscordContentLimit), true
}

// FitLolka returns Lolka markdown within the content limit.
func FitLolka(canonical string) (md string, truncated bool) {
	md = ForLolka(canonical)
	if UTF16Len(md) <= LolkaContentLimit {
		return md, false
	}
	return TruncateUTF16(md, LolkaContentLimit), true
}

func openTagName(tag string) (name string, closing bool) {
	tag = strings.TrimSpace(tag)
	if len(tag) < 3 || tag[0] != '<' || tag[len(tag)-1] != '>' {
		return "", false
	}
	inner := tag[1 : len(tag)-1]
	if strings.HasPrefix(inner, "/") {
		closing = true
		inner = inner[1:]
	}
	inner = strings.TrimSpace(inner)
	if i := strings.IndexAny(inner, " \t\n"); i >= 0 {
		inner = inner[:i]
	}
	return strings.ToLower(inner), closing
}

func truncateCanonicalKeepTags(canonical string, max int) string {
	var b strings.Builder
	var stack []string
	n := 0
	limit := max - 1
	i := 0
	for i < len(canonical) {
		if canonical[i] == '<' {
			end := strings.IndexByte(canonical[i:], '>')
			if end < 0 {
				break
			}
			tag := canonical[i : i+end+1]
			name, closing := openTagName(tag)
			// Always preserve the tag bytes so links/markup are not silently dropped.
			b.WriteString(tag)
			if name != "" {
				if closing {
					if len(stack) > 0 && stack[len(stack)-1] == name {
						stack = stack[:len(stack)-1]
					}
				} else {
					switch name {
					case "b", "i", "code", "pre", "spoiler", "a":
						stack = append(stack, name)
					}
				}
			}
			i += end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(canonical[i:])
		w := utf16.RuneLen(r)
		if w < 1 {
			w = 2
		}
		if n+w > limit {
			b.WriteRune('…')
			for j := len(stack) - 1; j >= 0; j-- {
				b.WriteString("</")
				b.WriteString(stack[j])
				b.WriteByte('>')
			}
			return b.String()
		}
		b.WriteRune(r)
		n += w
		i += size
	}
	return b.String()
}
