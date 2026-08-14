package prompt

import (
	"regexp"
	"strings"
)

// AbusiveWords is the list of curated profanity words to filter.
var AbusiveWords = []string{
	"madarchod",
	"mc",
	"bc",
	"benchod",
	"balatkar",
	"balatkari",
	"chutmarike",
	"chut",
}

// ContainsProfanity checks if the text contains any of the specified profanity words.
func ContainsProfanity(text string, customWords []string) bool {
	words := AbusiveWords
	if len(customWords) > 0 {
		words = customWords
	}

	lowerText := strings.ToLower(text)
	for _, w := range words {
		cleaned := strings.TrimSpace(strings.ToLower(w))
		if cleaned == "" {
			continue
		}
		pattern := `(?i)\b` + regexp.QuoteMeta(cleaned) + `\b`
		if matched, _ := regexp.MatchString(pattern, lowerText); matched {
			return true
		}
	}
	return false
}
