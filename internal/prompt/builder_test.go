package prompt

import (
	"strings"
	"testing"
)

func TestBuildDynamicSystemPrompt(t *testing.T) {
	params := SystemPromptParams{
		Identity: IdentityParams{
			Name:     "Chavi Sharma",
			Username: "Chavi396",
			Gender:   "female",
		},
		IsAdmin:          true,
		Username:         "john_doe",
		FirstName:        "John",
		AffectionScore:   65,
		RelationshipTier: "Close Friend",
		Intent:           "casual_chat",
		ShouldReact:      true,
		SuggestedEmoji:   "😂",
	}

	p := BuildDynamicSystemPrompt(params)

	if !strings.Contains(p, "Chavi Sharma") {
		t.Errorf("Expected prompt to contain bot name 'Chavi Sharma'")
	}
	if !strings.Contains(p, "Jaipur") {
		t.Errorf("Expected prompt to contain Jaipur location")
	}
	if !strings.Contains(p, "BCA") {
		t.Errorf("Expected prompt to contain BCA college background")
	}
	if !strings.Contains(p, "voice_script") {
		t.Errorf("Expected prompt to contain dual_output voice_script")
	}
	if !strings.Contains(p, "reply_text") {
		t.Errorf("Expected prompt to contain dual_output reply_text")
	}
}

func TestSanitizeInterlocutorName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"User", ""},
		{"user", ""},
		{"USERS", ""},
		{"Admin", ""},
		{"unknown", ""},
		{"none", ""},
		{"null", ""},
		{"telegram", ""},
		{"anonymous", ""},
		{"deleted account", ""},
		{"@", ""},
		{"-", ""},
		{"", ""},
		{"Sumit", "Sumit"},
		{"Rahul", "Rahul"},
		{"Priya", "Priya"},
		{"@Aman", "Aman"},
		{"  Rohit  ", "Rohit"},
	}

	for _, c := range cases {
		got := SanitizeInterlocutorName(c.input)
		if got != c.expected {
			t.Errorf("SanitizeInterlocutorName(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func TestBuildDynamicSystemPromptWithPlaceholderName(t *testing.T) {
	params := SystemPromptParams{
		Identity: IdentityParams{
			Name:     "Chavi Sharma",
			Username: "Chavi396",
			Gender:   "female",
		},
		Username:  "",
		FirstName: "User", // Telegram profile with placeholder name "User"
	}

	p := BuildDynamicSystemPrompt(params)

	if strings.Contains(p, `use their name "User"`) || strings.Contains(p, `use their name "user"`) {
		t.Errorf("Prompt should NEVER instruct bot to call interlocutor 'User'")
	}
	if strings.Contains(p, `You are chatting with "User"`) {
		t.Errorf("Prompt should not treat 'User' as a real name")
	}
	if !strings.Contains(p, `STRICT BAN ON THE WORD "USER"`) {
		t.Errorf("Prompt must include strict ban on the word USER")
	}
	if !strings.Contains(p, `ZERO PLACEHOLDER NAMES`) {
		t.Errorf("Prompt must include directive 5 banning placeholder names")
	}
}

func TestProfanityDetector(t *testing.T) {
	if !ContainsProfanity("tum ek madarchod ho", nil) {
		t.Errorf("Expected profanity to be detected")
	}
	if ContainsProfanity("hello how are you doing today", nil) {
		t.Errorf("Did not expect profanity to be detected")
	}
}

func TestChaviSelfiePromptBuilder(t *testing.T) {
	promptText := BuildChaviSelfiePrompt("wearing a yellow kurti sitting at cafe")
	if !strings.Contains(promptText, "Chavi from Jaipur") {
		t.Errorf("Expected selfie prompt to contain Chavi identity core, got: %s", promptText)
	}
	if !strings.Contains(promptText, "iPhone 15 Pro") {
		t.Errorf("Expected selfie prompt to contain iPhone camera tag, got: %s", promptText)
	}
	if !strings.Contains(promptText, "wearing a yellow kurti sitting at cafe") {
		t.Errorf("Expected selfie prompt to directly contain user request styling, got: %s", promptText)
	}
}

func TestNormalizeEmotionTags(t *testing.T) {
	input := "[exhausted sigh, soft laugh] yaar main thak gayi hoon 😊❤️"
	normalized := NormalizeEmotionTags(input)
	if !strings.HasPrefix(normalized, "[sigh]") && !strings.HasPrefix(normalized, "[laughing]") {
		t.Errorf("Expected normalized tag at start, got: %s", normalized)
	}
	if strings.Contains(normalized, "😊") || strings.Contains(normalized, "❤️") {
		t.Errorf("Expected emojis to be stripped from voice note, got: %s", normalized)
	}
}

func TestBuildDynamicSystemPromptPhotoDirective(t *testing.T) {
	params := SystemPromptParams{
		Identity: IdentityParams{
			Name:     "Chavi Sharma",
			Username: "Chavi396",
			Gender:   "female",
		},
		ExclusiveChannel: "photo_only",
		Intent:           "asking_photo",
	}

	p := BuildDynamicSystemPrompt(params)
	if !strings.Contains(p, "<active_channel_directive>") {
		t.Errorf("Expected active_channel_directive block in prompt")
	}
	if !strings.Contains(p, "YOU ARE SENDING A REAL PHOTO / SELFIE RIGHT NOW!") {
		t.Errorf("Expected photo sending instruction in prompt")
	}
	if !strings.Contains(p, "STRICT BAN ON REFUSAL / INSULTS") {
		t.Errorf("Expected strict ban on refusal/insults in photo directive")
	}
}

