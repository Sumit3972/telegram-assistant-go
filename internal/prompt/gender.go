package prompt

import (
	"regexp"
	"strings"
	"unicode"
)

// CleanHumanName cleans Telegram first and last names by removing emojis, formatting symbols,
// and system titles, returning a clean real name (e.g., "Riya Sharma" or "Aman").
func CleanHumanName(first, last string) string {
	cleanFirst := cleanNameToken(first)
	cleanLast := cleanNameToken(last)

	if cleanFirst != "" && cleanLast != "" {
		return cleanFirst + " " + cleanLast
	}
	if cleanFirst != "" {
		return cleanFirst
	}
	return cleanLast
}

func cleanNameToken(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ""
	}

	// Filter out emojis and non-alphabetic decorative symbols.
	// Keep Latin, Devanagari (Hindi), Gurmukhi, Cyrillic, spaces, hyphens.
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsSpace(r) || r == '-' || r == '\'' {
			b.WriteRune(r)
		}
	}
	cleaned := strings.Join(strings.Fields(b.String()), " ")
	if len([]rune(cleaned)) < 2 || len([]rune(cleaned)) > 35 {
		return ""
	}

	lower := strings.ToLower(cleaned)
	blacklist := map[string]bool{
		"user":            true,
		"users":           true,
		"admin":           true,
		"administrator":   true,
		"telegram":        true,
		"unknown":         true,
		"none":            true,
		"null":            true,
		"nil":             true,
		"anonymous":       true,
		"deleted":         true,
		"deleted account": true,
		"guest":           true,
		"member":          true,
		"bot":             true,
		"system":          true,
		"operator":        true,
		"moderator":       true,
		"mod":             true,
		"owner":           true,
		"test":            true,
		"testing":         true,
		"account":         true,
		"someone":         true,
		"somebody":        true,
		"profile":         true,
		"channel":         true,
		"group":           true,
	}

	if blacklist[lower] {
		return ""
	}

	return cleaned
}

// GetCallName returns the single first-name token to address the user casually in conversation.
// e.g. "Riya Sharma" -> "Riya", "Aman Verma" -> "Aman".
func GetCallName(fullName string) string {
	parts := strings.Fields(strings.TrimSpace(fullName))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// FormatUserDisplayName formats a display name prioritizing the user's real name (First + Last),
// falling back to username only if real name is absent.
func FormatUserDisplayName(firstName, lastName, username string) string {
	humanName := CleanHumanName(firstName, lastName)
	if humanName != "" {
		return humanName
	}
	cleanUname := SanitizeUsername(username)
	if cleanUname != "" {
		return cleanUname
	}
	return ""
}

var (
	femaleTextRegex = regexp.MustCompile(`(?i)\b(mai[n]? ladki hu[n]?|ladki hu[n]?|i am a girl|i'm a girl|i am female|i'm female|behan hu[n]?|girl hu[n]?|karti hu[n]?|karti hoon|aati hu[n]?|jati hu[n]?|so rahi hu[n]?|dekh rahi thi|kar rahi thi|bol rahi hu[n]?|thak gayi hu[n]?)\b`)
	maleTextRegex   = regexp.MustCompile(`(?i)\b(mai[n]? ladka hu[n]?|ladka hu[n]?|i am a boy|i'm a boy|i am male|i'm male|bhai hu[n]?|boy hu[n]?|karta hu[n]?|karta hoon|aata hu[n]?|jata hu[n]?|so raha hu[n]?|dekh raha tha|kar raha tha|bol raha hu[n]?|thak gaya hu[n]?)\b`)
)

// DetectGenderFromText checks if user explicitly identified their gender in chat or via first-person Hindi verbs.
func DetectGenderFromText(userMessage string) string {
	if userMessage == "" {
		return ""
	}
	if femaleTextRegex.MatchString(userMessage) {
		return "female"
	}
	if maleTextRegex.MatchString(userMessage) {
		return "male"
	}
	return ""
}
