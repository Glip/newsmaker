package format

import (
	"html"
	"regexp"
	"strings"
)

// Canonical markup uses a small HTML subset:
// <b> <i> <code> <pre> <a href="..."> <spoiler>

var (
	reTag = regexp.MustCompile(`(?is)</?(b|i|code|pre|spoiler|a)(\s+[^>]*)?>`)
)

func AppendSignature(text, signature string, enabled bool) string {
	text = strings.TrimSpace(text)
	signature = strings.TrimSpace(signature)
	if !enabled || signature == "" {
		return text
	}
	if text == "" {
		return signature
	}
	return text + "\n\n" + signature
}

func ForTelegramHTML(canonical string) string {
	out := canonical
	out = strings.ReplaceAll(out, "<spoiler>", "<tg-spoiler>")
	out = strings.ReplaceAll(out, "</spoiler>", "</tg-spoiler>")
	return out
}

func ForDiscord(canonical string) string {
	s := canonical
	s = replacePair(s, "<b>", "</b>", "**", "**")
	s = replacePair(s, "<i>", "</i>", "*", "*")
	s = replacePair(s, "<code>", "</code>", "`", "`")
	s = replacePair(s, "<pre>", "</pre>", "```\n", "\n```")
	s = replacePair(s, "<spoiler>", "</spoiler>", "||", "||")
	s = replaceAnchorsMD(s)
	return stripRemainingTags(s)
}

// ForLolka uses Discord-compatible markdown (Lolka webhooks accept the same content style).
func ForLolka(canonical string) string {
	return ForDiscord(canonical)
}

func ForVK(canonical string) string {
	// wall.post has no HTML/Markdown; format_data exists for messages.send only.
	// Strip tags to plain text; keep bare URLs so VK can autolink them.
	s := replaceAnchorsURL(canonical)
	s = stripRemainingTags(s)
	return html.UnescapeString(s)
}

func ForMAX(canonical string) string {
	// MAX: set format=html in NewMessageBody; tags: b/strong, i/em, u, s, code, pre, a, etc.
	out := canonical
	out = strings.ReplaceAll(out, "<spoiler>", "")
	out = strings.ReplaceAll(out, "</spoiler>", "")
	return out
}

func Plain(canonical string) string {
	s := replaceAnchorsPlain(canonical)
	s = stripRemainingTags(s)
	return html.UnescapeString(s)
}

func replacePair(s, open, close, replOpen, replClose string) string {
	for {
		start := strings.Index(strings.ToLower(s), open)
		if start < 0 {
			return s
		}
		end := strings.Index(strings.ToLower(s[start+len(open):]), close)
		if end < 0 {
			return s
		}
		end += start + len(open)
		inner := s[start+len(open) : end]
		s = s[:start] + replOpen + inner + replClose + s[end+len(close):]
	}
}

var reAnchor = regexp.MustCompile(`(?is)<a\s+href="([^"]+)"[^>]*>(.*?)</a>`)

func replaceAnchorsMD(s string) string {
	return reAnchor.ReplaceAllString(s, "[$2]($1)")
}

func replaceAnchorsPlain(s string) string {
	return reAnchor.ReplaceAllString(s, "$2 ($1)")
}

func replaceAnchorsURL(s string) string {
	// Prefer "label\nurl" so the URL stays clickable on the wall.
	return reAnchor.ReplaceAllStringFunc(s, func(m string) string {
		sub := reAnchor.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		href, label := sub[1], strings.TrimSpace(stripRemainingTags(sub[2]))
		if label == "" || label == href {
			return href
		}
		return label + "\n" + href
	})
}

func stripRemainingTags(s string) string {
	return reTag.ReplaceAllString(s, "")
}
