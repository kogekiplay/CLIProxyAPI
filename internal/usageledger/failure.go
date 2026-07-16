package usageledger

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxFailureTextBytes = 4096

var (
	authorizationHeaderRegex = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*)(?:bearer\s+)?[^\s,"'{}]+`)
	bearerTokenRegex         = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	apiKeyTokenRegex         = regexp.MustCompile(`(sk-proj-[A-Za-z0-9-_]{6,}|sk-ant-[A-Za-z0-9-_]{6,}|sk-[A-Za-z0-9-_]{6,}|sess-[A-Za-z0-9-_]{6,}|ghp_[A-Za-z0-9]{6,}|github_pat_[A-Za-z0-9_]{20,}|AIza[0-9A-Za-z-_]{8,}|hf_[A-Za-z0-9]{6,}|pk_[A-Za-z0-9]{6,}|rk_[A-Za-z0-9]{6,})`)
	tokenFieldRegex          = regexp.MustCompile(`(?i)\b(access_token|refresh_token|id_token)\b(\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+`)
	apiKeyFieldRegex         = regexp.MustCompile(`(?i)\b(api[-_ ]?key|x-api-key)\b(\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+`)
	cookieJSONFieldRegex     = regexp.MustCompile(`(?i)("?(?:cookie|set-cookie)"?\s*:\s*")[^"]*(")`)
	cookieHeaderRegex        = regexp.MustCompile(`(?i)\b(cookie|set-cookie)\s*:\s*[^,\r\n"}]+`)
	emailRegex               = regexp.MustCompile(`([A-Za-z0-9._%+\-])([A-Za-z0-9._%+\-]*)(@[A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
)

func failureSummaryFromBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if message := failureMessageFromJSON(body); message != "" {
		return sanitizeFailureText(message)
	}
	return sanitizeFailureText(body)
}

func sanitizeFailureText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = authorizationHeaderRegex.ReplaceAllString(text, `${1}[redacted]`)
	text = bearerTokenRegex.ReplaceAllString(text, `Bearer [redacted]`)
	text = tokenFieldRegex.ReplaceAllString(text, `${1}${2}[redacted]`)
	text = apiKeyFieldRegex.ReplaceAllString(text, `${1}${2}[redacted]`)
	text = apiKeyTokenRegex.ReplaceAllString(text, `[redacted]`)
	text = cookieJSONFieldRegex.ReplaceAllString(text, `${1}[redacted]${2}`)
	text = cookieHeaderRegex.ReplaceAllString(text, `${1}: [redacted]`)
	text = emailRegex.ReplaceAllString(text, `${1}***${3}`)
	return truncateFailureText(text)
}

// SanitizeFailureText removes common credentials and personal identifiers from failure text.
// It is exported for read-only views that must also sanitize historical ledger rows.
func SanitizeFailureText(text string) string {
	return sanitizeFailureText(text)
}

func failureMessageFromJSON(body string) string {
	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	return firstFailureMessage(payload)
}

func firstFailureMessage(value any) string {
	switch item := value.(type) {
	case map[string]any:
		for _, key := range []string{"message", "detail", "error_description", "code", "type"} {
			if text, ok := item[key].(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
		for _, key := range []string{"error", "errors"} {
			if text := firstFailureMessage(item[key]); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range item {
			if text := firstFailureMessage(child); text != "" {
				return text
			}
		}
	}
	return ""
}

func truncateFailureText(value string) string {
	if len(value) <= maxFailureTextBytes {
		return value
	}
	var builder strings.Builder
	for _, r := range value {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = len(string(r))
		}
		if builder.Len()+size > maxFailureTextBytes {
			break
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String()) + "..."
}
