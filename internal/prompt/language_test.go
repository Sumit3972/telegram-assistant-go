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

func TestSplitIntoDMBubbles(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantB1    string
		wantB2    string
		shouldSpl bool
	}{
		{
			name:      "Question break",
			input:     "sach me? mujhe to laga tum mazak kar rahe the haha",
			wantB1:    "sach me?",
			wantB2:    "mujhe to laga tum mazak kar rahe the haha",
			shouldSpl: true,
		},
		{
			name:      "Laugh emoji break",
			input:     "arre nahi yaar haha 😂 clg me lab practical tha to phone bag me hi reh gaya",
			wantB1:    "arre nahi yaar haha 😂",
			wantB2:    "clg me lab practical tha to phone bag me hi reh gaya",
			shouldSpl: true,
		},
		{
			name:      "Short message no split",
			input:     "haha haan sahi baat hai",
			wantB1:    "haha haan sahi baat hai",
			wantB2:    "",
			shouldSpl: false,
		},
		{
			name:      "Newline break",
			input:     "arre sun na\naaj MAU me lab practical bohot lamba chala gaya tha",
			wantB1:    "arre sun na",
			wantB2:    "aaj MAU me lab practical bohot lamba chala gaya tha",
			shouldSpl: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1, b2 := SplitIntoDMBubbles(tt.input)
			if tt.shouldSpl {
				if b2 == "" {
					t.Errorf("Expected split, got b1=%q, b2=%q", b1, b2)
				}
				if b1 != tt.wantB1 || b2 != tt.wantB2 {
					t.Errorf("Split mismatch:\n got: b1=%q, b2=%q\nwant: b1=%q, b2=%q", b1, b2, tt.wantB1, tt.wantB2)
				}
			} else {
				if b2 != "" {
					t.Errorf("Expected no split, got b2=%q", b2)
				}
			}
		})
	}
}


