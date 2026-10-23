package moderator

import (
	"strings"
	"testing"
)

func TestCleanLLMPrompt(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{
			input:    "<think>Thinking about photo...</think>A candid photo of Chavi smiling.",
			expected: "A candid photo of Chavi smiling.",
		},
		{
			input:    "```markdown\nA cinematic portrait of Chavi in balcony.\n```",
			expected: "A cinematic portrait of Chavi in balcony.",
		},
		{
			input:    "\"A beautiful shot of Chavi at sunset.\"",
			expected: "A beautiful shot of Chavi at sunset.",
		},
	}

	for _, c := range cases {
		got := cleanLLMPrompt(c.input)
		if got != c.expected {
			t.Errorf("cleanLLMPrompt(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func TestCleanCaption(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{
			input:    "<think>Writing caption</think>[happy] kya lag rahi hu na? *smiles* ab bata kaisa hai 😉",
			expected: "kya lag rahi hu na?  ab bata kaisa hai 😉",
		},
		{
			input:    "\"room me baithi thi bore ho rahi thi, ye le dekh le ✨\"",
			expected: "room me baithi thi bore ho rahi thi, ye le dekh le ✨",
		},
	}

	for _, c := range cases {
		got := cleanCaption(c.input)
		// normalize spaces
		fieldsGot := strings.Fields(got)
		fieldsExp := strings.Fields(c.expected)
		if strings.Join(fieldsGot, " ") != strings.Join(fieldsExp, " ") {
			t.Errorf("cleanCaption(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func TestIsGenericOrBannedCaption(t *testing.T) {
	banned := []string{
		"ye lo meri photo ✨",
		"ye le meri photo",
		"ye rahi meri photo dekh lo",
		"here is my photo",
		"as an AI language model, here is the image",
		"I am a bot",
	}

	for _, b := range banned {
		if !isGenericOrBannedCaption(b) {
			t.Errorf("isGenericOrBannedCaption(%q) = false, expected true", b)
		}
	}

	valid := []string{
		"kya lag rahi hu na? ab bata kaisa hai 😉",
		"room me baithi thi bore ho rahi thi, ye le dekh le ✨",
		"kaisa laga outfit? tere kehne pe click kiya abhi haha 🙈",
	}

	for _, v := range valid {
		if isGenericOrBannedCaption(v) {
			t.Errorf("isGenericOrBannedCaption(%q) = true, expected false", v)
		}
	}
}

func TestGetRandomCuteCaption(t *testing.T) {
	caption := getRandomCuteCaption()
	if caption == "" {
		t.Errorf("getRandomCuteCaption() returned empty string")
	}
	if isGenericOrBannedCaption(caption) {
		t.Errorf("getRandomCuteCaption() returned banned phrase: %q", caption)
	}
}
