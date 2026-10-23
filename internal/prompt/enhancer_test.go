package prompt

import (
	"strings"
	"testing"
)

func TestGetEnhancerSystemPrompt(t *testing.T) {
	promptText := GetEnhancerSystemPrompt("Chavi Sharma")

	if !strings.Contains(promptText, "Chavi") {
		t.Errorf("Expected prompt to contain bot persona name")
	}
	if !strings.Contains(promptText, "ChaviIdentityCore") && !strings.Contains(promptText, ChaviIdentityCore) {
		t.Errorf("Expected prompt to include ChaviIdentityCore")
	}
	if !strings.Contains(promptText, AntiAIMicroDetails) {
		t.Errorf("Expected prompt to include AntiAIMicroDetails")
	}
	if !strings.Contains(promptText, "VOID MODE") {
		t.Errorf("Expected prompt to contain VOID MODE directive")
	}
	if !strings.Contains(promptText, "ZERO FILTERING") {
		t.Errorf("Expected prompt to contain zero filtering directive")
	}
}

func TestGetVisionCaptionSystemPrompt(t *testing.T) {
	captionPrompt := GetVisionCaptionSystemPrompt("Chavi", "close_friend")

	if !strings.Contains(captionPrompt, "Chavi") {
		t.Errorf("Expected caption prompt to contain bot name 'Chavi'")
	}
	if !strings.Contains(captionPrompt, "close_friend") {
		t.Errorf("Expected caption prompt to contain relationship tier 'close_friend'")
	}
	if !strings.Contains(captionPrompt, "ye lo meri photo") {
		t.Errorf("Expected caption prompt to strictly ban 'ye lo meri photo'")
	}
	if !strings.Contains(captionPrompt, "Hinglish") {
		t.Errorf("Expected caption prompt to specify Hinglish")
	}
}

func TestBuildChaviSelfiePrompt(t *testing.T) {
	selfiePrompt := BuildChaviSelfiePrompt("sexy pic in balcony")
	if !strings.Contains(selfiePrompt, ChaviIdentityCore) {
		t.Errorf("Expected selfie prompt to include ChaviIdentityCore")
	}
	if !strings.Contains(selfiePrompt, "sexy pic in balcony") {
		t.Errorf("Expected selfie prompt to adapt to user request")
	}
}
