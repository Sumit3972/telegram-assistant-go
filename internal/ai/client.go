package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"telegram-ai-assistant/internal/domain"
)

type ProviderConfig struct {
	BaseURL string   `json:"base_url"`
	APIKey  string   `json:"api_key"`
	Models  []string `json:"models"`
}

type ClientConfig struct {
	Providers         []ProviderConfig
	FallbackProviders []ProviderConfig
	PerfRepo          domain.PerformanceRepository
}

type ModelCandidate struct {
	Provider ProviderConfig
	Model    string
}

type Client struct {
	providers         []ProviderConfig
	fallbackProviders []ProviderConfig
	perfRepo          domain.PerformanceRepository
	cooldownMgr       *CooldownManager
	httpClient        *http.Client
}

func NewClient(cfg ClientConfig) *Client {
	return &Client{
		providers:         cfg.Providers,
		fallbackProviders: cfg.FallbackProviders,
		perfRepo:          cfg.PerfRepo,
		cooldownMgr:       NewCooldownManager(),
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

type ChatCompletionOptions struct {
	Tools            []domain.ToolDefinition `json:"tools,omitempty"`
	ToolChoice       any                     `json:"tool_choice,omitempty"`
	Temperature      *float64                `json:"temperature,omitempty"`
	ResponseFormat   map[string]any          `json:"response_format,omitempty"`
	ForceProviderURL string                  `json:"-"`
	ForceModel       string                  `json:"-"`
}

type ChatCompletionResult struct {
	Message     domain.ChatMessage
	ModelUsed   string
	ProviderURL string
}

func (c *Client) ChatCompletions(ctx context.Context, messages []domain.ChatMessage, opts ChatCompletionOptions) (*ChatCompletionResult, error) {
	// 1. Forced provider/model check
	if opts.ForceProviderURL != "" && opts.ForceModel != "" {
		for _, p := range append(c.providers, c.fallbackProviders...) {
			if p.BaseURL == opts.ForceProviderURL {
				msg, err := c.request(ctx, p.BaseURL, p.APIKey, opts.ForceModel, messages, opts)
				if err == nil {
					return &ChatCompletionResult{
						Message:     *msg,
						ModelUsed:   opts.ForceModel,
						ProviderURL: opts.ForceProviderURL,
					}, nil
				}
				log.Printf("[AIClient Forced] Forced model %s failed: %v", opts.ForceModel, err)
			}
		}
	}

	// 2. Assemble primary candidates strictly using the configured env APIKey
	var primaryCandidates []ModelCandidate
	for _, provider := range c.providers {
		for _, m := range provider.Models {
			primaryCandidates = append(primaryCandidates, ModelCandidate{
				Provider: provider,
				Model:    m,
			})
		}
	}

	res, err := c.executeCandidates(ctx, primaryCandidates, messages, opts)
	if err == nil {
		return res, nil
	}

	log.Printf("[AIClient] Primary candidates failed: %v. Attempting fallbacks...", err)

	// 3. Try fallback providers if configured
	if len(c.fallbackProviders) > 0 {
		var fallbackCandidates []ModelCandidate
		for _, fb := range c.fallbackProviders {
			for _, m := range fb.Models {
				fallbackCandidates = append(fallbackCandidates, ModelCandidate{
					Provider: fb,
					Model:    m,
				})
			}
		}
		fbRes, fbErr := c.executeCandidates(ctx, fallbackCandidates, messages, opts)
		if fbErr == nil {
			return fbRes, nil
		}
		return nil, fmt.Errorf("all primary and fallback AI models failed: %w (fallback: %v)", err, fbErr)
	}

	return nil, err
}

func (c *Client) executeCandidates(
	ctx context.Context,
	candidates []ModelCandidate,
	messages []domain.ChatMessage,
	opts ChatCompletionOptions,
) (*ChatCompletionResult, error) {
	var available []ModelCandidate
	for _, cand := range candidates {
		if !c.cooldownMgr.IsOnCooldown(cand.Provider.BaseURL, cand.Model, cand.Provider.APIKey) {
			available = append(available, cand)
		}
	}

	toTry := available
	if len(toTry) == 0 {
		toTry = candidates
	}

	// Detect if vision request
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

	var lastErr error
	for i, cand := range toTry {
		// Skip text-only models for vision requests
		if isVision {
			lm := strings.ToLower(cand.Model)
			if strings.Contains(lm, "deepseek") || strings.Contains(lm, "minimax") || strings.Contains(lm, "zinc") || strings.Contains(lm, "hy3") || strings.Contains(lm, "grok-composer") || strings.Contains(lm, "doubao-seed") {
				continue
			}
		}

		startTime := time.Now()
		log.Printf("[AIClient] Attempting completion via %s with model %s (candidate %d/%d)...", cand.Provider.BaseURL, cand.Model, i+1, len(toTry))

		msg, err := c.request(ctx, cand.Provider.BaseURL, cand.Provider.APIKey, cand.Model, messages, opts)
		latency := time.Since(startTime).Milliseconds()

		if err == nil {
			if c.perfRepo != nil {
				go func(pURL, m string, lat int64) {
					_ = c.perfRepo.RecordPerformance(context.Background(), pURL, m, true, lat)
				}(cand.Provider.BaseURL, cand.Model, latency)
			}
			return &ChatCompletionResult{
				Message:     *msg,
				ModelUsed:   cand.Model,
				ProviderURL: cand.Provider.BaseURL,
			}, nil
		}

		lastErr = err
		log.Printf("[AIClient] Model %s (%s) failed (%dms): %v", cand.Model, cand.Provider.BaseURL, latency, err)
		c.cooldownMgr.PutOnCooldown(cand.Provider.BaseURL, cand.Model, 1*time.Minute, cand.Provider.APIKey)

		if i < len(toTry)-1 {
			log.Printf("⏳ [AIClient] Waiting 5s before switching to fallback model...")
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no candidate models succeeded")
}

func (c *Client) request(
	ctx context.Context,
	baseURL, apiKey, model string,
	messages []domain.ChatMessage,
	opts ChatCompletionOptions,
) (*domain.ChatMessage, error) {
	cleanURL := strings.TrimRight(baseURL, "/") + "/chat/completions"

	reqBody := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   false,
	}

	if len(opts.Tools) > 0 {
		reqBody["tools"] = opts.Tools
		if opts.ToolChoice != nil {
			reqBody["tool_choice"] = opts.ToolChoice
		}
	} else if opts.ResponseFormat != nil {
		reqBody["response_format"] = opts.ResponseFormat
	}

	if opts.Temperature != nil {
		reqBody["temperature"] = *opts.Temperature
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cleanURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("HTTP-Referer", "https://github.com/RooVetGit/Roo-Code")
	httpReq.Header.Set("X-Title", "Roo Code")
	if strings.Contains(cleanURL, "agentrouter") {
		httpReq.Header.Set("User-Agent", "RooCode/3.34.8")
	} else {
		httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	}
	httpReq.Header.Set("Accept", "application/json, text/plain, */*")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		bodyStr := string(bodyBytes)
		if len(bodyStr) > 400 {
			bodyStr = bodyStr[:400] + "..."
		}
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, bodyStr)
	}

	bodyText := strings.TrimSpace(string(bodyBytes))
	var dataMap map[string]any

	if strings.HasPrefix(bodyText, "data:") {
		lines := strings.Split(bodyText, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				jsonStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if jsonStr != "[DONE]" {
					if err := json.Unmarshal([]byte(jsonStr), &dataMap); err == nil {
						break
					}
				}
			}
		}
	} else {
		_ = json.Unmarshal(bodyBytes, &dataMap)
	}

	if dataMap == nil {
		return nil, fmt.Errorf("invalid json response from AI: %s", bodyText)
	}

	if errObj, ok := dataMap["error"]; ok && errObj != nil {
		return nil, fmt.Errorf("AI API error: %v", errObj)
	}

	choices, ok := dataMap["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil, fmt.Errorf("invalid response format (no choices): %s", bodyText)
	}

	choiceMap, ok := choices[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid choice object: %v", choices[0])
	}

	msgMap, ok := choiceMap["message"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid message object: %v", choiceMap)
	}

	msgJSON, _ := json.Marshal(msgMap)
	var chatMsg domain.ChatMessage
	if err := json.Unmarshal(msgJSON, &chatMsg); err != nil {
		return nil, fmt.Errorf("failed to parse ChatMessage: %w", err)
	}

	return &chatMsg, nil
}
