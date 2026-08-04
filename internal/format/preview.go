package format

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

type PlatformPreview struct {
	Platform string `json:"platform"`
	Label    string `json:"label"`
	HTML     string `json:"html"`
	Note     string `json:"note,omitempty"`
}

// Previews builds browser-safe HTML previews for each platform from canonical markup.
// hasMedia selects Telegram caption (1024) vs text (4096) limit.
func Previews(canonical string, hasMedia bool) []PlatformPreview {
	tgMax := TelegramTextLimit
	tgLimitName := "текст"
	if hasMedia {
		tgMax = TelegramCaptionLimit
		tgLimitName = "caption"
	}
	tgCut, tgTrunc := TruncateCanonical(canonical, tgMax)
	tgNote := ""
	vis := VisibleUTF16Len(canonical)
	if tgTrunc {
		tgNote = fmt.Sprintf("Обрезано до %d символов (лимит Telegram %s). Хвост, включая ссылки в конце, не отправится.", tgMax, tgLimitName)
	} else if hasMedia && vis >= tgMax*9/10 {
		tgNote = fmt.Sprintf("Лимит caption с медиа: %d (сейчас %d)", tgMax, vis)
	}

	dsCut, dsTrunc := FitDiscord(canonical)
	dsNote := ""
	if dsTrunc {
		dsNote = fmt.Sprintf("Обрезано до %d символов (лимит Discord)", DiscordContentLimit)
	}

	lkCut, lkTrunc := FitLolka(canonical)
	lkNote := ""
	if lkTrunc {
		lkNote = fmt.Sprintf("Обрезано до %d символов (лимит LOLKA)", LolkaContentLimit)
	}

	return []PlatformPreview{
		{
			Platform: "telegram",
			Label:    "Telegram",
			HTML:     htmlPreviewFromTags(ForTelegramHTML(tgCut), true),
			Note:     tgNote,
		},
		{
			Platform: "discord",
			Label:    "Discord",
			HTML:     discordMarkdownToPreviewHTML(dsCut),
			Note:     dsNote,
		},
		{
			Platform: "lolka",
			Label:    "LOLKA",
			HTML:     discordMarkdownToPreviewHTML(lkCut),
			Note:     lkNote,
		},
		{
			Platform: "max",
			Label:    "MAX",
			HTML:     htmlPreviewFromTags(ForMAX(canonical), false),
		},
		{
			Platform: "vk",
			Label:    "VK",
			HTML:     plainToPreviewHTML(ForVK(canonical)),
			Note:     "Стена VK не поддерживает жирный/курсив — только обычный текст",
		},
	}
}

var (
	reBR  = regexp.MustCompile(`\r\n|\r|\n`)
	reURL = regexp.MustCompile(`https?://[^\s<]+`)
)

func htmlPreviewFromTags(s string, keepSpoiler bool) string {
	if strings.TrimSpace(s) == "" {
		return `<span class="preview-empty">Пусто</span>`
	}
	// Escape everything, then restore only our known tags.
	escaped := html.EscapeString(s)
	// Unescape allowed tags back (they were escaped as &lt;b&gt; etc.)
	replacer := []struct{ from, to string }{
		{"&lt;b&gt;", "<b>"}, {"&lt;/b&gt;", "</b>"},
		{"&lt;i&gt;", "<i>"}, {"&lt;/i&gt;", "</i>"},
		{"&lt;code&gt;", "<code>"}, {"&lt;/code&gt;", "</code>"},
		{"&lt;pre&gt;", "<pre>"}, {"&lt;/pre&gt;", "</pre>"},
	}
	if keepSpoiler {
		replacer = append(replacer,
			struct{ from, to string }{"&lt;tg-spoiler&gt;", `<span class="spoiler">`},
			struct{ from, to string }{"&lt;/tg-spoiler&gt;", "</span>"},
			struct{ from, to string }{"&lt;spoiler&gt;", `<span class="spoiler">`},
			struct{ from, to string }{"&lt;/spoiler&gt;", "</span>"},
		)
	}
	for _, r := range replacer {
		escaped = strings.ReplaceAll(escaped, r.from, r.to)
		escaped = strings.ReplaceAll(escaped, strings.ToUpper(r.from), r.to)
	}
	// Restore <a href="..."> — only http(s)
	escaped = restoreAnchors(escaped)
	escaped = reBR.ReplaceAllString(escaped, "<br>")
	return escaped
}

var reEscapedAnchor = regexp.MustCompile(`(?is)&lt;a\s+href=&#34;([^&]+)&#34;[^&]*&gt;(.*?)&lt;/a&gt;`)

func restoreAnchors(escaped string) string {
	return reEscapedAnchor.ReplaceAllStringFunc(escaped, func(m string) string {
		sub := reEscapedAnchor.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		href := html.UnescapeString(sub[1])
		u, err := url.Parse(href)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return sub[2]
		}
		return `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener">` + sub[2] + `</a>`
	})
}

func discordMarkdownToPreviewHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return `<span class="preview-empty">Пусто</span>`
	}
	s := html.EscapeString(md)
	// Order matters: code fences, then inline, then bold, italic, spoiler, links
	s = regexp.MustCompile("(?s)```\\n?([\\s\\S]*?)```").ReplaceAllString(s, "<pre>$1</pre>")
	s = regexp.MustCompile("`([^`]+)`").ReplaceAllString(s, "<code>$1</code>")
	s = regexp.MustCompile(`(?s)\|\|(.+?)\|\|`).ReplaceAllString(s, `<span class="spoiler">$1</span>`)
	s = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(s, "<b>$1</b>")
	s = regexp.MustCompile(`(?m)(^|[^*])\*([^*\n]+)\*`).ReplaceAllString(s, "$1<i>$2</i>")
	s = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`).ReplaceAllString(s, `<a href="$2" target="_blank" rel="noopener">$1</a>`)
	s = reBR.ReplaceAllString(s, "<br>")
	return s
}

func plainToPreviewHTML(s string) string {
	if strings.TrimSpace(s) == "" {
		return `<span class="preview-empty">Пусто</span>`
	}
	esc := html.EscapeString(s)
	esc = reURL.ReplaceAllStringFunc(esc, func(u string) string {
		return `<a href="` + u + `" target="_blank" rel="noopener">` + u + `</a>`
	})
	esc = reBR.ReplaceAllString(esc, "<br>")
	return esc
}
