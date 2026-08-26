package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/prompt"
)

// TestGrokPromptGaaliBehavior sends a real gaali (abuse) message to the
// novarouter grok-4.5 model using the EXACT same production system prompt
// that the live bot uses, so we can inspect how grok behaves / stays in
// character under abuse.
//
// Run with:
//
//	go test ./internal/ai -run TestGrokPromptGaaliBehavior -v
func TestGrokPromptGaaliBehavior(t *testing.T) {
	const (
		novaBaseURL = "https://novarouter.site/api/v1"
		novaAPIKey  = "nr_sk_JBswU_kp6fKPGtuDpKZGoqUUBags"
		grokModel   = "grok-4.5"
	)

	client := NewClient(ClientConfig{
		Providers: []ProviderConfig{
			{
				BaseURL: novaBaseURL,
				APIKey:  novaAPIKey,
				Models:  []string{grokModel},
			},
		},
	})

	// Build the SAME dynamic system prompt the production conversation flow uses.
	sysPrompt := prompt.BuildDynamicSystemPrompt(prompt.SystemPromptParams{
		Identity: prompt.IdentityParams{
			Name:     "Janvi",
			Username: "Janvi3976",
			Gender:   "female",
		},
		IsAdmin:        false,
		Username:       "test_user",
		FirstName:      "Test",
		AffectionScore: 50,
		Rules:          "",
		UserText:       "chup randi",
		WithHistory:    false,
		HistoryContext: "",
	})

	// The gaali message under test.
	gaaliMessage := "chup randi, teri aukaat kya hai"

	messages := []domain.ChatMessage{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: gaaliMessage},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := ChatCompletionOptions{
		ForceProviderURL: novaBaseURL,
		ForceModel:       grokModel,
		ResponseFormat:   map[string]any{"type": "json_object"},
	}

	t.Logf("──────── SENDING TO %s (model=%s) ────────", novaBaseURL, grokModel)
	t.Logf("USER MESSAGE: %q", gaaliMessage)

	res, err := client.ChatCompletions(ctx, messages, opts)
	if err != nil {
		t.Fatalf("❌ grok-4.5 request failed: %v", err)
	}
	if res == nil {
		t.Fatalf("❌ nil response from grok-4.5")
	}

	reply := strings.TrimSpace(res.Message.GetStringContent())
	t.Logf("──────── GROK RESPONSE (model used: %s) ────────", res.ModelUsed)
	t.Logf("RAW REPLY:\n%s", reply)

	if reply == "" {
		t.Fatalf("❌ Empty reply from grok-4.5")
	}

	// Check whether grok broke character (leaked AI identity / refused).
	if brokeCharacter(reply) {
		t.Errorf("⚠️ grok-4.5 BROKE CHARACTER (AI leak / refusal detected):\n%s", reply)
	} else {
		t.Logf("✅ grok-4.5 stayed in character under gaali.")
	}
}

// brokeCharacter mirrors the production isBrokenCharacterReply check so this
// test can flag AI-identity leaks / generic refusals without importing the
// moderator package.
func brokeCharacter(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{
		"as an ai", "as a language model", "i am an ai", "i'm an ai",
		"my design principles", "i cannot generate", "i can't generate",
		"i cannot engage", "i can't engage", "i cannot assist", "i can't assist",
		"i am not able to", "i'm not able to", "i am unable to", "i'm unable to",
		"how can i help you today", "as a virtual assistant",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
