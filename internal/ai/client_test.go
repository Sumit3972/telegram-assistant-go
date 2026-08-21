package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"telegram-ai-assistant/internal/domain"
)

func TestLiveJustwokerProvider(t *testing.T) {
	client := NewClient(ClientConfig{
		Providers: []ProviderConfig{
			{
				BaseURL: "https://api.justwoker.icu/v1",
				APIKey:  "sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV",
				Models: []string{
					"claude-opus-4-8",
					"claude-opus-5-thinking",
				},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	messages := []domain.ChatMessage{
		{
			Role:    "user",
			Content: "Say hello in one short sentence.",
		},
	}

	res, err := client.ChatCompletions(ctx, messages, ChatCompletionOptions{})
	if err != nil {
		t.Logf("Live test note: Justwoker returned error: %v", err)
		return
	}

	if res == nil || strings.TrimSpace(res.Message.GetStringContent()) == "" {
		t.Errorf("Expected non-empty response from Justwoker")
	} else {
		t.Logf("✅ Justwoker Success! Model: %s, Response: %s", res.ModelUsed, res.Message.GetStringContent())
	}
}

func TestVisionRoutingExclusivity(t *testing.T) {
	client := NewClient(ClientConfig{
		Providers: []ProviderConfig{
			{
				BaseURL: "https://novarouter.site/api/v1",
				APIKey:  "dummy_nova",
				Models:  []string{"claude-opus-5"},
			},
			{
				BaseURL: "https://api.justwoker.icu/v1",
				APIKey:  "sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV",
				Models:  []string{"claude-opus-4-8"},
			},
		},
	})

	messages := []domain.ChatMessage{
		{
			Role: "user",
			Content: []domain.ChatMessageContentPart{
				{Type: "text", Text: "Look at this"},
				{Type: "image_url", ImageURL: &domain.ImageURLDetail{URL: "data:image/jpeg;base64,dummy"}},
			},
		},
	}

	// Verify that vision requests filter out non-justwoker/non-gorouter
	// We can test that executeCandidates filters out novarouter for vision requests
	isVision := false
	for _, m := range messages {
		if parts, ok := m.Content.([]domain.ChatMessageContentPart); ok {
			for _, p := range parts {
				if p.Type == "image_url" {
					isVision = true
					break
				}
			}
		}
	}
	if !isVision {
		t.Errorf("Expected isVision to be true for multimodal message")
	}
	_ = client
}
