package publish

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"newsmaker/internal/channels"
)

var (
	reDiscordWebhook     = regexp.MustCompile(`(?i)discord(?:app)?\.com/api/webhooks/(\d+)/`)
	reDiscordChannelLink = regexp.MustCompile(`(?i)(?:https?://)?(?:ptb\.|canary\.)?discord(?:app)?\.com/channels/(\d+)/(\d+)(?:/(\d+))?`)
	reLolkaChannelLink   = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?lolka\.app/servers/(\d+)/channels/(\d+)(?:/(\d+))?`)
)

// BuildPostURL builds a public permalink when the platform allows it.
// messageRef is the publisher's opaque id (or an absolute URL already).
func BuildPostURL(platform channels.Platform, targetID, credential, messageRef string) string {
	messageRef = strings.TrimSpace(messageRef)
	if messageRef == "" || messageRef == "ok" {
		return ""
	}
	if strings.HasPrefix(messageRef, "https://") || strings.HasPrefix(messageRef, "http://") {
		return messageRef
	}
	switch platform {
	case channels.PlatformTelegram:
		return telegramURL(targetID, messageRef)
	case channels.PlatformVK:
		return vkURL(targetID, messageRef)
	case channels.PlatformDiscord:
		return discordURL(targetID, messageRef)
	case channels.PlatformLolka:
		return lolkaURL(targetID, messageRef)
	case channels.PlatformMAX:
		// MAX mid is not a stable public web URL for channels.
		return ""
	default:
		return ""
	}
}

func telegramURL(chatID, msgID string) string {
	chatID = strings.TrimSpace(chatID)
	msgID = strings.TrimSpace(msgID)
	if chatID == "" || msgID == "" {
		return ""
	}
	if strings.HasPrefix(chatID, "@") {
		return fmt.Sprintf("https://t.me/%s/%s", strings.TrimPrefix(chatID, "@"), msgID)
	}
	// Private/supergroup/channel numeric id: -100xxxxxxxxxx → t.me/c/xxxxxxxxxx/msg
	id := chatID
	if strings.HasPrefix(id, "-100") {
		id = strings.TrimPrefix(id, "-100")
	} else if strings.HasPrefix(id, "-") {
		id = strings.TrimPrefix(id, "-")
	}
	if id == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/c/%s/%s", id, msgID)
}

func vkURL(ownerID, postID string) string {
	ownerID = strings.TrimSpace(ownerID)
	postID = strings.TrimSpace(postID)
	if ownerID == "" || postID == "" {
		return ""
	}
	if _, err := strconv.ParseInt(postID, 10, 64); err != nil {
		return ""
	}
	return fmt.Sprintf("https://vk.com/wall%s_%s", ownerID, postID)
}

// ParseDiscordChannelLink extracts guild and channel ids from a Discord message/channel URL.
// Example: https://discord.com/channels/376016913517510658/729070328378294363/1533784852430848061
func ParseDiscordChannelLink(s string) (guildID, channelID string, ok bool) {
	m := reDiscordChannelLink.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 3 || m[1] == "" || m[2] == "" {
		return "", "", false
	}
	return m[1], m[2], true
}

// ValidDiscordChannelLink reports whether s looks like a Discord channel/message permalink.
func ValidDiscordChannelLink(s string) bool {
	_, _, ok := ParseDiscordChannelLink(s)
	return ok
}

func discordURL(sampleLink, messageRef string) string {
	guildID, channelID, ok := ParseDiscordChannelLink(sampleLink)
	if !ok {
		return ""
	}
	msgID := strings.TrimSpace(messageRef)
	if msgID == "" || msgID == "ok" {
		return ""
	}
	// Bare snowflake message id.
	if _, err := strconv.ParseUint(msgID, 10, 64); err != nil {
		return ""
	}
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, msgID)
}

// ParseLolkaChannelLink extracts server and channel ids from a Lolka message/channel URL.
// Example: https://lolka.app/servers/690630853756928/channels/690637693962240/820964766368768
func ParseLolkaChannelLink(s string) (serverID, channelID string, ok bool) {
	m := reLolkaChannelLink.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 3 || m[1] == "" || m[2] == "" {
		return "", "", false
	}
	return m[1], m[2], true
}

func lolkaURL(sampleLink, messageRef string) string {
	serverID, channelID, ok := ParseLolkaChannelLink(sampleLink)
	if !ok {
		return ""
	}
	msgID := strings.TrimSpace(messageRef)
	if msgID == "" || msgID == "ok" {
		return ""
	}
	if _, err := strconv.ParseUint(msgID, 10, 64); err != nil {
		return ""
	}
	return fmt.Sprintf("https://lolka.app/servers/%s/channels/%s/%s", serverID, channelID, msgID)
}

// DiscordWebhookChannelHint extracts webhook id (not channel id) — unused for links.
func DiscordWebhookChannelHint(webhookURL string) string {
	m := reDiscordWebhook.FindStringSubmatch(webhookURL)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func TruncatePreview(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func SafeURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return u
}
