package prompt

import (
	"strings"
	"testing"
)

func TestBuildDynamicSystemPrompt(t *testing.T) {
	params := SystemPromptParams{
		Identity: IdentityParams{
			Name:     "Janvi",
			Username: "Janvi3976",
			Gender:   "female",
		},
		IsAdmin:        true,
		Username:       "john_doe",
		FirstName:      "John",
		AffectionScore: 65,
		Rules:          "1. Be respectful\n2. No spam",
		UserText:       "photo bhej apni",
		WithHistory:    true,
		HistoryContext: "john_doe: hello\nJanvi: hey there!",
		EmojiListStr:   "👍, ❤️, 🔥",
	}

	p := BuildDynamicSystemPrompt(params)

	if !strings.Contains(p, "Janvi") {
		t.Errorf("Expected prompt to contain bot name 'Janvi'")
	}
	if !strings.Contains(p, "photo bhej") && !strings.Contains(p, "selfie_generation") && !strings.Contains(p, "selfie_prompt") {
		t.Errorf("Expected prompt to include selfie generation directives")
	}
	if !strings.Contains(p, "mute_user") {
		t.Errorf("Expected prompt to contain admin moderation tools")
	}
	if !strings.Contains(p, "65%") {
		t.Errorf("Expected prompt to contain affection score 65%%")
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

func TestVoiceAndSelfieKeywords(t *testing.T) {
	if !IsVoiceRequested("mujhe voice note sunao") {
		t.Errorf("Expected voice request to be detected")
	}
	if !IsSelfieRequested("apni ek cute selfie dikha") {
		t.Errorf("Expected selfie request to be detected")
	}
}
