package prompt

import (
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"English anger", "I dont speak hindi stupid bitch", "en"},
		{"English emotional", "Lonliness has followed me everywhere", "en"},
		{"English question", "What is the meaning of your bio ..?", "en"},
		{"English short", "She???", "en"},
		{"English warning", "Stay away from my sis ok you bich", "en"},
		{"Hinglish group", "Munna naye aye ho kya tg pr?", "hinglish"},
		{"Hinglish DM invite", "Tum ekk baar dm ma aao", "hinglish"},
		{"Hinglish casual", "kya kar rahi ho aaj", "hinglish"},
		{"Hinglish greeting", "subah subah dimaag khane aa gaya", "hinglish"},
		{"Hinglish no photo", "meri photo nahi hai mere pas", "hinglish"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectLanguage(tt.input)
			if got != tt.expected {
				t.Errorf("DetectLanguage(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestIsStandaloneEmoji(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"❤️", true},
		{"😂", true},
		{"🔥", true},
		{"🥺", true},
		{"👍", true},
		{"💔", true},
		{"😐", true},
		{"hello ❤️", false},
		{"kya hua 😂", false},
		{"cool", false},
		{"", false},
		{"   ", false},
	}

	for _, tt := range tests {
		got := IsStandaloneEmoji(tt.input)
		if got != tt.expected {
			t.Errorf("IsStandaloneEmoji(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestReactionEmojis(t *testing.T) {
	if len(ReactionCriteria) == 0 {
		t.Fatal("ReactionCriteria must not be empty")
	}

	for key := range ReactionCriteria {
		emoji, ok := ReactionEmojiMap[key]
		if !ok || emoji == "" {
			t.Errorf("ReactionEmojiMap missing key: %s", key)
		}
	}

	preCriteria := PreDecisionReactionCriteria()
	if preCriteria["none"] != "No reaction" {
		t.Errorf("PreDecisionReactionCriteria missing 'none' key")
	}
	if len(preCriteria) != len(ReactionCriteria)+1 {
		t.Errorf("PreDecisionReactionCriteria length mismatch: got %d, want %d", len(preCriteria), len(ReactionCriteria)+1)
	}

	defEmoji := DefaultReactionEmoji()
	if defEmoji == "" {
		t.Error("DefaultReactionEmoji returned empty string")
	}
}

