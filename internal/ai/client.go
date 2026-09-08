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
	"sync"
	"time"

	"telegram-ai-assistant/internal/domain"
)

type ProviderConfig struct {
	BaseURL       string   `json:"base_url"`
	APIKey        string   `json:"api_key"`
	Models        []string `json:"models"`
	DynamicModels bool     `json:"dynamic_models"`
	UseAnthropic  bool     `json:"use_anthropic"`
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
	providers          []ProviderConfig
	fallbackProviders  []ProviderConfig
	perfRepo           domain.PerformanceRepository
	cooldownMgr        *CooldownManager
	httpClient         *http.Client
	dynamicModelsCache map[string][]string
	dynamicModelsMu    sync.RWMutex
}

func NewClient(cfg ClientConfig) *Client {
	return &Client{
		providers:          cfg.Providers,
		fallbackProviders:  cfg.FallbackProviders,
		perfRepo:           cfg.PerfRepo,
		cooldownMgr:        NewCooldownManager(),
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
		dynamicModelsCache: make(map[string][]string),
	}
}

type ChatCompletionOptions struct {
	Tools            []domain.ToolDefinition `json:"tools,omitempty"`
	ToolChoice       any                     `json:"tool_choice,omitempty"`
	Temperature      *float64                `json:"temperature,omitempty"`
	MaxTokens        *int                    `json:"max_tokens,omitempty"`
	ResponseFormat   map[string]any          `json:"response_format,omitempty"`
	ForceProviderURL string                  `json:"-"`
	ForceModel       string                  `json:"-"`
	ExcludeModel     string                  `json:"-"`
}

type ChatCompletionResult struct {
	Message     domain.ChatMessage
	ModelUsed   string
	ProviderURL string
}

type ModelItem struct {
	ID string `json:"id"`
}

type ModelsResponse struct {
	Data []ModelItem `json:"data"`
}

// FetchModels queries <baseURL>/models to retrieve current available models.
func (c *Client) FetchModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	cleanURL := strings.TrimRight(baseURL, "/") + "/models"

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, cleanURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request for models: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Origin", "https://trae.ai")
	httpReq.Header.Set("Referer", "https://trae.ai/")
	httpReq.Header.Set("HTTP-Referer", "https://trae.ai")
	httpReq.Header.Set("X-Title", "Trae")
	if strings.Contains(cleanURL, "alwaysdata") || strings.Contains(cleanURL, "agentrouter") {
		httpReq.Header.Set("Originator", "codex_cli_rs")
		httpReq.Header.Set("User-Agent", "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464")
		httpReq.Header.Set("Version", "0.101.0")
	} else {
		httpReq.Header.Set("User-Agent", "Trae/1.0.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	}
	httpReq.Header.Set("Accept", "application/json, text/plain, */*")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request error fetching models: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read models response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		bodyStr := string(bodyBytes)
		if len(bodyStr) > 300 {
			bodyStr = bodyStr[:300] + "..."
		}
		return nil, fmt.Errorf("HTTP error %d fetching models: %s", resp.StatusCode, bodyStr)
	}

	var respObj ModelsResponse
	if err := json.Unmarshal(bodyBytes, &respObj); err != nil {
		return nil, fmt.Errorf("failed to parse models json: %w", err)
	}

	var models []string
	for _, item := range respObj.Data {
		trimmed := strings.TrimSpace(item.ID)
		if trimmed != "" {
			models = append(models, trimmed)
		}
	}

	if len(models) == 0 {
		return nil, errors.New("no models returned from models endpoint")
	}

	return models, nil
}

func (c *Client) resolveProviderModels(ctx context.Context, p ProviderConfig) []string {
	if !p.DynamicModels {
		return p.Models
	}

	models, err := c.FetchModels(ctx, p.BaseURL, p.APIKey)
	if err == nil && len(models) > 0 {
		c.dynamicModelsMu.Lock()
		c.dynamicModelsCache[p.BaseURL] = models
		c.dynamicModelsMu.Unlock()
		log.Printf("[AIClient DynamicModels] Fetched %d active models from %s: %v", len(models), p.BaseURL, models)
		return models
	}

	log.Printf("[AIClient DynamicModels] Warning: failed to fetch models from %s: %v. Checking cache/fallback.", p.BaseURL, err)

	c.dynamicModelsMu.RLock()
	cached, ok := c.dynamicModelsCache[p.BaseURL]
	c.dynamicModelsMu.RUnlock()
	if ok && len(cached) > 0 {
		log.Printf("[AIClient DynamicModels] Using %d cached models for %s: %v", len(cached), p.BaseURL, cached)
		return cached
	}

	return p.Models
}

func (c *Client) ChatCompletions(ctx context.Context, messages []domain.ChatMessage, opts ChatCompletionOptions) (*ChatCompletionResult, error) {
	// 1. Forced provider/model check
	if opts.ForceProviderURL != "" && opts.ForceModel != "" {
		for _, p := range append(c.providers, c.fallbackProviders...) {
			if p.BaseURL == opts.ForceProviderURL {
				var msg *domain.ChatMessage
				var err error
				if p.UseAnthropic || strings.Contains(strings.ToLower(p.BaseURL), "justwoker") {
					msg, err = c.requestAnthropic(ctx, p.BaseURL, p.APIKey, opts.ForceModel, messages, opts)
				} else {
					msg, err = c.request(ctx, p.BaseURL, p.APIKey, opts.ForceModel, messages, opts)
					if err != nil && (strings.Contains(err.Error(), "403") || strings.Contains(strings.ToLower(err.Error()), "cloudflare")) {
						msg, err = c.requestAnthropic(ctx, p.BaseURL, p.APIKey, opts.ForceModel, messages, opts)
					}
				}
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
		models := c.resolveProviderModels(ctx, provider)
		for _, m := range models {
			if opts.ExcludeModel != "" && m == opts.ExcludeModel {
				continue
			}
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
			models := c.resolveProviderModels(ctx, fb)
			for _, m := range models {
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
		// For vision/image processing requests, exclusively use Justwoker and Gorouter providers
		if isVision {
			bu := strings.ToLower(cand.Provider.BaseURL)
			if !strings.Contains(bu, "justwoker") && !strings.Contains(bu, "gorouter") {
				continue
			}
		}

		startTime := time.Now()
		log.Printf("[AIClient] Attempting completion via %s with model %s (candidate %d/%d)...", cand.Provider.BaseURL, cand.Model, i+1, len(toTry))

		var msg *domain.ChatMessage
		var err error
		if cand.Provider.UseAnthropic || strings.Contains(strings.ToLower(cand.Provider.BaseURL), "justwoker") {
			msg, err = c.requestAnthropic(ctx, cand.Provider.BaseURL, cand.Provider.APIKey, cand.Model, messages, opts)
		} else {
			msg, err = c.request(ctx, cand.Provider.BaseURL, cand.Provider.APIKey, cand.Model, messages, opts)
			if err != nil && (strings.Contains(err.Error(), "403") || strings.Contains(strings.ToLower(err.Error()), "cloudflare")) {
				log.Printf("[AIClient] Provider %s hit Cloudflare 403 on /chat/completions; retrying with Anthropic /messages protocol...", cand.Provider.BaseURL)
				msg, err = c.requestAnthropic(ctx, cand.Provider.BaseURL, cand.Provider.APIKey, cand.Model, messages, opts)
			}
		}
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
	}
	if opts.ResponseFormat != nil {
		reqBody["response_format"] = opts.ResponseFormat
	}

	if opts.Temperature != nil {
		reqBody["temperature"] = *opts.Temperature
	}
	if opts.MaxTokens != nil {
		reqBody["max_tokens"] = *opts.MaxTokens
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
	httpReq.Header.Set("Origin", "https://trae.ai")
	httpReq.Header.Set("Referer", "https://trae.ai/")
	httpReq.Header.Set("HTTP-Referer", "https://trae.ai")
	httpReq.Header.Set("X-Title", "Trae")
	if strings.Contains(cleanURL, "alwaysdata") || strings.Contains(cleanURL, "agentrouter") {
		httpReq.Header.Set("Originator", "codex_cli_rs")
		httpReq.Header.Set("User-Agent", "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464")
		httpReq.Header.Set("Version", "0.101.0")
	} else {
		httpReq.Header.Set("User-Agent", "Trae/1.0.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
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

func (c *Client) requestAnthropic(
	ctx context.Context,
	baseURL, apiKey, model string,
	messages []domain.ChatMessage,
	opts ChatCompletionOptions,
) (*domain.ChatMessage, error) {
	cleanURL := strings.TrimRight(baseURL, "/") + "/messages"

	var systemPrompt string
	var anthropicMsgs []map[string]any

	for _, m := range messages {
		if m.Role == "system" {
			systemPrompt = m.GetStringContent()
			continue
		}

		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}

		switch content := m.Content.(type) {
		case string:
			anthropicMsgs = append(anthropicMsgs, map[string]any{
				"role":    role,
				"content": content,
			})
		case []domain.ChatMessageContentPart:
			var parts []map[string]any
			for _, p := range content {
				if p.Type == "text" {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": p.Text,
					})
				} else if p.Type == "image_url" && p.ImageURL != nil {
					url := p.ImageURL.URL
					if strings.HasPrefix(url, "data:") {
						semicolon := strings.Index(url, ";")
						comma := strings.Index(url, ",")
						if semicolon > 5 && comma > semicolon {
							mediaType := url[5:semicolon]
							data := url[comma+1:]
							parts = append(parts, map[string]any{
								"type": "image",
								"source": map[string]any{
									"type":       "base64",
									"media_type": mediaType,
									"data":       data,
								},
							})
						}
					}
				}
			}
			anthropicMsgs = append(anthropicMsgs, map[string]any{
				"role":    role,
				"content": parts,
			})
		default:
			anthropicMsgs = append(anthropicMsgs, map[string]any{
				"role":    role,
				"content": m.GetStringContent(),
			})
		}
	}

	mt := 2048
	if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
		mt = *opts.MaxTokens
	}

	reqBody := map[string]any{
		"model":      model,
		"messages":   anthropicMsgs,
		"max_tokens": mt,
	}
	if systemPrompt != "" {
		reqBody["system"] = systemPrompt
	}
	if opts.Temperature != nil {
		reqBody["temperature"] = *opts.Temperature
	}

	if len(opts.Tools) > 0 {
		var tools []map[string]any
		for _, t := range opts.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Function.Name,
				"description":  t.Function.Description,
				"input_schema": t.Function.Parameters,
			})
		}
		reqBody["tools"] = tools
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cleanURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create anthropic request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Origin", "https://trae.ai")
	httpReq.Header.Set("Referer", "https://trae.ai/")
	httpReq.Header.Set("HTTP-Referer", "https://trae.ai")
	httpReq.Header.Set("X-Title", "Trae")
	httpReq.Header.Set("User-Agent", "Trae/1.0.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	httpReq.Header.Set("Accept", "application/json, text/plain, */*")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read anthropic response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		bodyStr := string(bodyBytes)
		if len(bodyStr) > 400 {
			bodyStr = bodyStr[:400] + "..."
		}
		return nil, fmt.Errorf("Anthropic HTTP error %d: %s", resp.StatusCode, bodyStr)
	}

	var dataMap map[string]any
	if err := json.Unmarshal(bodyBytes, &dataMap); err != nil {
		return nil, fmt.Errorf("invalid json from Anthropic: %w", err)
	}

	if errObj, ok := dataMap["error"]; ok && errObj != nil {
		return nil, fmt.Errorf("Anthropic API error: %v", errObj)
	}

	contentArr, ok := dataMap["content"].([]any)
	if !ok || len(contentArr) == 0 {
		return nil, fmt.Errorf("no content in anthropic response: %s", string(bodyBytes))
	}

	var textParts []string
	var toolCalls []domain.ToolCall

	for _, item := range contentArr {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := itemMap["type"].(string)
		if itemType == "text" {
			if t, ok := itemMap["text"].(string); ok {
				textParts = append(textParts, t)
			}
		} else if itemType == "tool_use" {
			tID, _ := itemMap["id"].(string)
			tName, _ := itemMap["name"].(string)
			inputJSON, _ := json.Marshal(itemMap["input"])
			toolCalls = append(toolCalls, domain.ToolCall{
				ID:   tID,
				Type: "function",
				Function: domain.FunctionCall{
					Name:      tName,
					Arguments: string(inputJSON),
				},
			})
		}
	}

	return &domain.ChatMessage{
		Role:      "assistant",
		Content:   strings.Join(textParts, ""),
		ToolCalls: toolCalls,
	}, nil
}
