package prompt

import (
	"strings"
)

var VoiceKeywords = []string{
	"voice", "audio", "awaaz", "avaz", "avaj", "awaj", "speak", "say", "bol", "sun", "suna", "sunao",
	"sunaye", "voice message", "voice note", "talk", "speech", "vocal", "listen", "bhej voice",
	"voice bhej", "sound", "batao", "baat karo", "waaz", "awaz", "bata",
}

// IsVoiceRequested checks if the user's message indicates a desire to hear audio/voice.
func IsVoiceRequested(text string) bool {
	lower := strings.ToLower(text)
	for _, kw := range VoiceKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
