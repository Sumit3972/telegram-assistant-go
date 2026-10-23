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
