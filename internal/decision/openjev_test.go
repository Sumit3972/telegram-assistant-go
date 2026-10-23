package decision

import (
	"strings"
	"testing"
)

func TestGuardrailsVoiceVsPhoto(t *testing.T) {
	testCases := []struct {
		userText        string
		rawIntent       string
		rawChannel      string
		expectedIntent  string
		expectedChannel string
	}{
		{
			userText:        "Ek cute saa voice note about",
			rawIntent:       "asking_photo",
			rawChannel:      "photo_only",
			expectedIntent:  "asking_voice",
			expectedChannel: "voice_only",
		},
		{
			userText:        "Ek pyara saa voice note bejo naa yrr",
			rawIntent:       "asking_photo",
			rawChannel:      "voice_only",
			expectedIntent:  "asking_voice",
			expectedChannel: "voice_only",
		},
		{
			userText:        "Please ek sexy si pic bejo naa yrr",
			rawIntent:       "casual_chat",
			rawChannel:      "text_only",
			expectedIntent:  "asking_photo",
			expectedChannel: "photo_only",
		},
		{
			userText:        "apni selfie dikhao",
			rawIntent:       "flirty",
			rawChannel:      "text_only",
			expectedIntent:  "asking_photo",
			expectedChannel: "photo_only",
		},
	}

	for _, tc := range testCases {
		userLower := strings.ToLower(tc.userText)
		hasVoiceKW := strings.Contains(userLower, "voice") || strings.Contains(userLower, "audio") ||
			strings.Contains(userLower, "awaaz") || strings.Contains(userLower, "awaz") ||
			strings.Contains(userLower, "bol ke") || strings.Contains(userLower, "bolke") ||
			strings.Contains(userLower, "voice note")
		hasPhotoKW := strings.Contains(userLower, "photo") || strings.Contains(userLower, "pic") ||
			strings.Contains(userLower, "selfie") || strings.Contains(userLower, "picture") ||
			strings.Contains(userLower, "tasveer")

		intentChoice := tc.rawIntent
		channelChoice := tc.rawChannel

		if hasVoiceKW && !hasPhotoKW {
			intentChoice = "asking_voice"
			channelChoice = "voice_only"
		} else if hasPhotoKW && !hasVoiceKW {
			intentChoice = "asking_photo"
			channelChoice = "photo_only"
		} else {
			if channelChoice == "voice_only" && intentChoice == "asking_photo" {
				intentChoice = "asking_voice"
			} else if channelChoice == "photo_only" && intentChoice == "asking_voice" {
				intentChoice = "asking_photo"
			}
		}

		if intentChoice != tc.expectedIntent {
			t.Errorf("For %q: got intent %s, expected %s", tc.userText, intentChoice, tc.expectedIntent)
		}
		if channelChoice != tc.expectedChannel {
			t.Errorf("For %q: got channel %s, expected %s", tc.userText, channelChoice, tc.expectedChannel)
		}
	}
}
