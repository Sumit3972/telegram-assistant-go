package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"telegram-ai-assistant/internal/domain"
)

var (
	DefaultClientID     = os.Getenv("ANTIGRAVITY_CLIENT_ID")
	DefaultClientSecret = os.Getenv("ANTIGRAVITY_CLIENT_SECRET")
)

const (
	DefaultUserAgent = "Antigravity/4.3.0 (Windows NT 10.0; Win64; x64) Chrome/132.0.6834.160 Electron/39.2.3"
	TokenURL         = "https://oauth2.googleapis.com/token"


	BaseURLDaily = "https://daily-cloudcode-pa.googleapis.com/v1internal"

	ModelGemini38Flash     = "gemini-3.8-flash-high"
	ModelGemini37Flash     = "gemini-3.7-flash"
	ModelGemini37FlashHigh = "gemini-3.7-flash-high"
	ModelGemini36Flash     = "gemini-3.6-flash"
	ModelClaudeSonnet      = "claude-sonnet-4-6"
	ModelClaudeOpus        = "claude-opus-4-6-thinking"
	ModelImagen3           = "gemini-3.1-flash-image"
)

type AccountRecord struct {
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
}

type AccountState struct {
	Email               string
	RefreshToken        string
	AccessToken         string
	TokenExpiry         time.Time
	ProjectID           string
	AuthCooldownTill    time.Time
	GeminiCooldownTill  time.Time
	ClaudeCooldownTill  time.Time
	ImageCooldownTill   time.Time
	GeminiQuotaFraction float64
	ClaudeQuotaFraction float64
	ImageQuotaFraction  float64
	QuotaLastChecked    time.Time
	mu                  sync.Mutex
}

func (acc *AccountState) PutOnCooldown(taskType string, duration time.Duration) {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	until := time.Now().Add(duration)
	switch taskType {
	case "gemini":
		acc.GeminiCooldownTill = until
	case "claude":
		acc.ClaudeCooldownTill = until
	case "image":
		acc.ImageCooldownTill = until
	case "auth":
		acc.AuthCooldownTill = until
	default:
		acc.GeminiCooldownTill = until
		acc.ClaudeCooldownTill = until
		acc.ImageCooldownTill = until
	}
}

func (acc *AccountState) IsOnCooldown(taskType string) bool {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	now := time.Now()
	if now.Before(acc.AuthCooldownTill) {
		return true
	}
	switch taskType {
	case "gemini":
		return now.Before(acc.GeminiCooldownTill)
	case "claude":
		return now.Before(acc.ClaudeCooldownTill)
	case "image":
		return now.Before(acc.ImageCooldownTill)
	default:
		return now.Before(acc.GeminiCooldownTill) && now.Before(acc.ClaudeCooldownTill)
	}
}

type Client struct {
	accounts     []*AccountState
	clientID     string
	clientSecret string
	userAgent    string
	httpClient   *http.Client
	roundRobin   int
	poolMu       sync.Mutex
}

type Config struct {
	AccountsJSON  string
	RefreshToken  string
	Email         string
	AccountsPath  string
	AccountsFiles []string
}

func parseAccountsString(raw string) []AccountRecord {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var res []AccountRecord
	// 1. Try parsing JSON array: [{"email":"...", "refresh_token":"..."}]
	if err := json.Unmarshal([]byte(raw), &res); err == nil && len(res) > 0 {
		return res
	}
	// 2. Try parsing single JSON object: {"email":"...", "refresh_token":"..."}
	var single AccountRecord
	if err := json.Unmarshal([]byte(raw), &single); err == nil && strings.TrimSpace(single.RefreshToken) != "" {
		return []AccountRecord{single}
	}
	// 3. Delimited string: "email:token,email:token" or newline separated
	items := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if parts := strings.SplitN(item, ":", 2); len(parts) == 2 {
			res = append(res, AccountRecord{
				Email:        strings.TrimSpace(parts[0]),
				RefreshToken: strings.TrimSpace(parts[1]),
			})
		} else {
			res = append(res, AccountRecord{
				RefreshToken: item,
			})
		}
	}
	return res
}

func NewClient(cfg Config) *Client {
	clientID := DefaultClientID
	if clientID == "" {
		clientID = os.Getenv("ANTIGRAVITY_CLIENT_ID")
	}
	clientSecret := DefaultClientSecret
	if clientSecret == "" {
		clientSecret = os.Getenv("ANTIGRAVITY_CLIENT_SECRET")
	}

	c := &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		userAgent:    DefaultUserAgent,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}

	seenTokens := make(map[string]bool)
	seenEmails := make(map[string]bool)
	var records []AccountRecord

	addAccount := func(email, refreshToken string) {
		rt := strings.TrimSpace(refreshToken)
		em := strings.ToLower(strings.TrimSpace(email))
		if rt == "" || seenTokens[rt] {
			return
		}
		if em != "" && seenEmails[em] {
			return
		}
		seenTokens[rt] = true
		if em != "" {
			seenEmails[em] = true
		}
		records = append(records, AccountRecord{Email: email, RefreshToken: rt})
	}

	// 1. Check AccountsJSON from config or ANTIGRAVITY_ACCOUNTS env
	accountsEnv := cfg.AccountsJSON
	if accountsEnv == "" {
		accountsEnv = os.Getenv("ANTIGRAVITY_ACCOUNTS")
	}
	for _, acc := range parseAccountsString(accountsEnv) {
		addAccount(acc.Email, acc.RefreshToken)
	}

	// 2. Check numbered environment variables: ANTIGRAVITY_REFRESH_TOKEN_1, ANTIGRAVITY_EMAIL_1, etc.
	for i := 1; i <= 50; i++ {
		rt := os.Getenv(fmt.Sprintf("ANTIGRAVITY_REFRESH_TOKEN_%d", i))
		em := os.Getenv(fmt.Sprintf("ANTIGRAVITY_EMAIL_%d", i))
		if rt != "" {
			addAccount(em, rt)
		}
	}

	// 3. Gather candidate file paths (1.json, 2.json, accounts.json)
	candidates := []string{"1.json", "2.json", "accounts.json"}
	if cfg.AccountsPath != "" && cfg.AccountsPath != "accounts.json" {
		candidates = append([]string{cfg.AccountsPath}, candidates...)
	}
	for _, f := range cfg.AccountsFiles {
		if f != "" {
			candidates = append(candidates, f)
		}
	}

	for _, filePath := range candidates {
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var list []AccountRecord
		if err := json.Unmarshal(data, &list); err == nil && len(list) > 0 {
			for _, item := range list {
				addAccount(item.Email, item.RefreshToken)
			}
			continue
		}

		var single AccountRecord
		if err := json.Unmarshal(data, &single); err == nil && strings.TrimSpace(single.RefreshToken) != "" {
			addAccount(single.Email, single.RefreshToken)
		}
	}

	// 4. Also check standalone environment fallback
	singleRT := cfg.RefreshToken
	if singleRT == "" {
		singleRT = os.Getenv("ANTIGRAVITY_REFRESH_TOKEN")
	}
	singleEmail := cfg.Email
	if singleEmail == "" {
		singleEmail = os.Getenv("ANTIGRAVITY_EMAIL")
	}
	if strings.TrimSpace(singleRT) != "" {
		addAccount(singleEmail, singleRT)
	}


	// Initialize AccountState objects with default healthy quota estimates
	for _, rec := range records {
		c.accounts = append(c.accounts, &AccountState{
			Email:               rec.Email,
			RefreshToken:        rec.RefreshToken,
			GeminiQuotaFraction: 0.9,
			ClaudeQuotaFraction: 1.0,
			ImageQuotaFraction:  0.9,
		})
	}

	log.Printf("[Antigravity] Initialized account pool with %d unique account(s):", len(c.accounts))
	for i, acc := range c.accounts {
		log.Printf("  -> [%d] %s", i+1, acc.Email)
	}

	// Persist consolidated accounts to accounts.json
	if len(records) > 0 {
		if enc, err := json.MarshalIndent(records, "", "  "); err == nil {
			_ = os.WriteFile("accounts.json", enc, 0644)
		}
	}

	return c
}

func (c *Client) IsConfigured() bool {
	return len(c.accounts) > 0
}

func (c *Client) AccountCount() int {
	return len(c.accounts)
}

func isModelCapacityError(statusCode int, bodyStr string) bool {
	if statusCode == 503 {
		return true
	}
	s := strings.ToUpper(bodyStr)
	return strings.Contains(s, "MODEL_CAPACITY_EXHAUSTED") ||
		strings.Contains(s, "NO CAPACITY AVAILABLE") ||
		strings.Contains(s, "CAPACITY_EXHAUSTED") ||
		strings.Contains(s, "UNAVAILABLE") ||
		strings.Contains(s, "OVERLOADED")
}

func isQuotaOrRateLimitError(statusCode int, bodyStr string) bool {
	if statusCode == 429 {
		return true
	}
	s := strings.ToUpper(bodyStr)
	return strings.Contains(s, "RESOURCE_EXHAUSTED") ||
		strings.Contains(s, "QUOTA") ||
		strings.Contains(s, "RATE_LIMIT") ||
		strings.Contains(s, "EXHAUSTED")
}

func isAuthError(statusCode int, bodyStr string) bool {
	if statusCode == 401 {
		return true
	}
	s := strings.ToLower(bodyStr)
	return strings.Contains(s, "invalid_grant") || strings.Contains(s, "unauthorized")
}

func isSafetyBlockedError(statusCode int, bodyStr string) bool {
	s := strings.ToUpper(bodyStr)
	return strings.Contains(s, "SAFETY") ||
		strings.Contains(s, "HARM_CATEGORY") ||
		strings.Contains(s, "BLOCKLIST") ||
		strings.Contains(s, "RECITATION") ||
		strings.Contains(s, "PROMPT_BLOCKED")
}

// FetchQuotaFor contacts Google Cloud Code API to inspect live quota fractions.
func (c *Client) FetchQuotaFor(ctx context.Context, acc *AccountState) error {
	token, err := c.getAccessTokenFor(ctx, acc)
	if err != nil {
		return err
	}
	projectID := c.getProjectIDFor(ctx, acc, token)

	fetchReqBody, _ := json.Marshal(map[string]any{"project": projectID})
	fetchReq, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURLDaily+":fetchAvailableModels", bytes.NewReader(fetchReqBody))
	if err != nil {
		return err
	}
	fetchReq.Header.Set("Authorization", "Bearer "+token)
	fetchReq.Header.Set("User-Agent", c.userAgent)
	fetchReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(fetchReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetchAvailableModels HTTP %d", resp.StatusCode)
	}

	var modelsRes struct {
		Models map[string]struct {
			QuotaInfo struct {
				RemainingFraction float64 `json:"remainingFraction"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&modelsRes); err != nil {
		return err
	}

	acc.mu.Lock()
	defer acc.mu.Unlock()
	acc.QuotaLastChecked = time.Now()

	if m, ok := modelsRes.Models[ModelGemini38Flash]; ok {
		acc.GeminiQuotaFraction = m.QuotaInfo.RemainingFraction
	}
	if m, ok := modelsRes.Models[ModelClaudeSonnet]; ok {
		acc.ClaudeQuotaFraction = m.QuotaInfo.RemainingFraction
	}
	if m, ok := modelsRes.Models[ModelImagen3]; ok {
		acc.ImageQuotaFraction = m.QuotaInfo.RemainingFraction
	}

	return nil
}

// SyncAllQuotas refreshes quotas across all configured accounts.
func (c *Client) SyncAllQuotas(ctx context.Context) {
	for i, acc := range c.accounts {
		if err := c.FetchQuotaFor(ctx, acc); err != nil {
			log.Printf("[Antigravity] ⚠️ Could not fetch quotas for %s: %v", acc.Email, err)
		} else {
			acc.mu.Lock()
			log.Printf("[Antigravity] 📊 Account %d (%s) Quotas -> Gemini 3.8: %.1f%% | Claude 4.6: %.1f%% | Imagen 3: %.1f%%",
				i+1, acc.Email,
				acc.GeminiQuotaFraction*100.0,
				acc.ClaudeQuotaFraction*100.0,
				acc.ImageQuotaFraction*100.0,
			)
			acc.mu.Unlock()
		}
	}
}

// getAccountsForTask retrieves accounts not on cooldown for the requested task,
// prioritized by remaining quota fraction (e.g. accounts with 95.8% and 90.1% first, 34.8% as backup).
func (c *Client) getAccountsForTask(taskType string) []*AccountState {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()

	var available []*AccountState
	for _, acc := range c.accounts {
		if !acc.IsOnCooldown(taskType) {
			available = append(available, acc)
		}
	}

	if len(available) == 0 {
		log.Printf("[Antigravity] ⚠️ All accounts on cooldown for task '%s'. Resetting cooldowns and retrying all...", taskType)
		for _, acc := range c.accounts {
			acc.mu.Lock()
			switch taskType {
			case "gemini":
				acc.GeminiCooldownTill = time.Time{}
			case "claude":
				acc.ClaudeCooldownTill = time.Time{}
			case "image":
				acc.ImageCooldownTill = time.Time{}
			default:
				acc.GeminiCooldownTill = time.Time{}
				acc.ClaudeCooldownTill = time.Time{}
				acc.ImageCooldownTill = time.Time{}
			}
			acc.mu.Unlock()
		}
		available = append(available, c.accounts...)
	}

	// Sort accounts by remaining quota fraction descending
	sort.SliceStable(available, func(i, j int) bool {
		available[i].mu.Lock()
		quotaI := available[i].GeminiQuotaFraction
		if taskType == "claude" {
			quotaI = available[i].ClaudeQuotaFraction
		} else if taskType == "image" {
			quotaI = available[i].ImageQuotaFraction
		}
		available[i].mu.Unlock()

		available[j].mu.Lock()
		quotaJ := available[j].GeminiQuotaFraction
		if taskType == "claude" {
			quotaJ = available[j].ClaudeQuotaFraction
		} else if taskType == "image" {
			quotaJ = available[j].ImageQuotaFraction
		}
		available[j].mu.Unlock()

		return quotaI > quotaJ
	})

	// If top accounts have close quotas (difference < 15%), alternate them via round-robin
	if len(available) > 1 {
		c.roundRobin = (c.roundRobin + 1) % len(available)

		available[0].mu.Lock()
		topQ := available[0].GeminiQuotaFraction
		if taskType == "claude" {
			topQ = available[0].ClaudeQuotaFraction
		} else if taskType == "image" {
			topQ = available[0].ImageQuotaFraction
		}
		available[0].mu.Unlock()

		available[1].mu.Lock()
		secondQ := available[1].GeminiQuotaFraction
		if taskType == "claude" {
			secondQ = available[1].ClaudeQuotaFraction
		} else if taskType == "image" {
			secondQ = available[1].ImageQuotaFraction
		}
		available[1].mu.Unlock()

		if (topQ-secondQ) < 0.15 && c.roundRobin%2 == 1 {
			available[0], available[1] = available[1], available[0]
		}
	}

	return available
}

func (c *Client) getAccessTokenFor(ctx context.Context, acc *AccountState) (string, error) {
	acc.mu.Lock()
	defer acc.mu.Unlock()

	if acc.AccessToken != "" && time.Now().Add(5*time.Minute).Before(acc.TokenExpiry) {
		return acc.AccessToken, nil
	}

	data := url.Values{}
	data.Set("client_id", c.clientID)
	data.Set("client_secret", c.clientSecret)
	data.Set("refresh_token", acc.RefreshToken)
	data.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange failed HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var res struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return "", err
	}

	acc.AccessToken = res.AccessToken
	exp := res.ExpiresIn
	if exp <= 0 {
		exp = 3600
	}
	acc.TokenExpiry = time.Now().Add(time.Duration(exp) * time.Second)
	return acc.AccessToken, nil
}

func (c *Client) getProjectIDFor(ctx context.Context, acc *AccountState, token string) string {
	acc.mu.Lock()
	if acc.ProjectID != "" {
		pid := acc.ProjectID
		acc.mu.Unlock()
		return pid
	}
	acc.mu.Unlock()

	reqBody, _ := json.Marshal(map[string]any{"metadata": map[string]any{"ideType": "ANTIGRAVITY"}})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURLDaily+":loadCodeAssist", bytes.NewReader(reqBody))
	if err == nil {
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("User-Agent", c.userAgent)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := c.httpClient.Do(httpReq)
		if err == nil {
			respBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var assistRes struct {
				CloudAICompanionProject string `json:"cloudaicompanionProject"`
			}
			if err := json.Unmarshal(respBytes, &assistRes); err == nil && assistRes.CloudAICompanionProject != "" {
				acc.mu.Lock()
				acc.ProjectID = assistRes.CloudAICompanionProject
				acc.mu.Unlock()
				return assistRes.CloudAICompanionProject
			}
		}
	}

	return "aicode-consumers"
}

type ChatOptions struct {
	Model       string
	Temperature *float64
	MaxTokens   *int
}

func (c *Client) buildModelHierarchy(requested string) []string {
	reqLower := strings.ToLower(requested)
	var list []string

	if strings.Contains(reqLower, "claude") {
		list = []string{
			ModelClaudeSonnet,
			ModelClaudeOpus,
			ModelGemini38Flash,
			ModelGemini37Flash,
			ModelGemini36Flash,
		}
	} else {
		if requested != "" {
			list = append(list, requested)
		}
		// Primary fast conversational default
		list = append(list, ModelGemini38Flash)
		// Fallbacks: 3.7, 3.6, and Claude reasoning fallback
		list = append(list,
			ModelGemini37Flash,
			ModelGemini37FlashHigh,
			ModelGemini36Flash,
			ModelClaudeSonnet,
		)
	}

	seen := make(map[string]bool)
	var models []string
	for _, m := range list {
		clean := strings.TrimSpace(m)
		if clean != "" && !seen[clean] {
			seen[clean] = true
			models = append(models, clean)
		}
	}
	return models
}

// Complete generates chat completions with prioritized multi-account rotation and failover.
func (c *Client) Complete(
	ctx context.Context,
	messages []domain.ChatMessage,
	opts ChatOptions,
) (*domain.ChatMessage, string, error) {
	if len(c.accounts) == 0 {
		return nil, "", errors.New("no antigravity accounts configured")
	}

	modelsToTry := c.buildModelHierarchy(opts.Model)

	// Format messages
	var systemParts []map[string]any
	var contents []map[string]any

	for _, msg := range messages {
		if msg.Role == "system" {
			if textStr, ok := msg.Content.(string); ok && strings.TrimSpace(textStr) != "" {
				systemParts = append(systemParts, map[string]any{"text": textStr})
			}
			continue
		}

		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}

		var parts []map[string]any
		switch v := msg.Content.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				parts = append(parts, map[string]any{"text": v})
			}
		case []domain.ChatMessageContentPart:
			for _, part := range v {
				if part.Type == "text" && part.Text != "" {
					parts = append(parts, map[string]any{"text": part.Text})
				} else if part.Type == "image_url" && part.ImageURL != nil {
					dataURL := part.ImageURL.URL
					if strings.HasPrefix(dataURL, "data:") {
						commaIdx := strings.Index(dataURL, ",")
						if commaIdx > 0 {
							mime := strings.TrimPrefix(dataURL[:commaIdx], "data:")
							mime = strings.TrimSuffix(mime, ";base64")
							parts = append(parts, map[string]any{
								"inlineData": map[string]any{
									"mimeType": mime,
									"data":     dataURL[commaIdx+1:],
								},
							})
						}
					}
				}
			}
		}

		if len(parts) > 0 {
			contents = append(contents, map[string]any{
				"role":  role,
				"parts": parts,
			})
		}
	}

	if len(contents) == 0 {
		return nil, "", errors.New("no message contents provided")
	}

	var lastErr error

	taskType := "gemini"
	if strings.Contains(strings.ToLower(opts.Model), "claude") {
		taskType = "claude"
	}

	accounts := c.getAccountsForTask(taskType)
	if len(accounts) == 0 {
		return nil, "", errors.New("no antigravity accounts available")
	}

	// Account outer loop: try each account
	// Model inner loop: try fallback models (3.8 -> 3.7 -> 3.6 -> claude) on the SAME account before changing accounts
	for accIdx, acc := range accounts {
		token, err := c.getAccessTokenFor(ctx, acc)
		if err != nil {
			log.Printf("[Antigravity] ⚠️ Auth error for %s: %v. Putting on 15m cooldown...", acc.Email, err)
			acc.PutOnCooldown("auth", 15*time.Minute)
			lastErr = err
			continue
		}

		projectID := c.getProjectIDFor(ctx, acc, token)

		accountSuccess := false
		for modelIdx, model := range modelsToTry {
			// Skip Claude on this account if claude is specifically on cooldown
			if strings.Contains(model, "claude") && acc.IsOnCooldown("claude") {
				continue
			}

			log.Printf("[Antigravity] Attempting model %s on account %s (%d/%d accounts, model %d/%d)...",
				model, acc.Email, accIdx+1, len(accounts), modelIdx+1, len(modelsToTry))

			reqBody := map[string]any{
				"project":   projectID,
				"model":     model,
				"userAgent": "antigravity",
				"requestId": fmt.Sprintf("req-%d", time.Now().UnixMilli()),
				"request": map[string]any{
					"contents": contents,
					"generationConfig": map[string]any{
						"temperature":     0.85,
						"maxOutputTokens": 2048,
					},
				},
			}

			if len(systemParts) > 0 {
				innerReq := reqBody["request"].(map[string]any)
				innerReq["systemInstruction"] = map[string]any{
					"role":  "user",
					"parts": systemParts,
				}
			}

			if opts.Temperature != nil {
				genConfig := reqBody["request"].(map[string]any)["generationConfig"].(map[string]any)
				genConfig["temperature"] = *opts.Temperature
			}
			if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
				genConfig := reqBody["request"].(map[string]any)["generationConfig"].(map[string]any)
				genConfig["maxOutputTokens"] = *opts.MaxTokens
			}

			jsonBytes, _ := json.Marshal(reqBody)

			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURLDaily+":streamGenerateContent?alt=sse", bytes.NewReader(jsonBytes))
			if err != nil {
				lastErr = err
				continue
			}

			httpReq.Header.Set("Authorization", "Bearer "+token)
			httpReq.Header.Set("User-Agent", c.userAgent)
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("x-client-name", "antigravity")
			httpReq.Header.Set("x-client-version", "4.3.0")
			if strings.Contains(model, "claude") {
				httpReq.Header.Set("anthropic-beta", "claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14")
			}

			resp, err := c.httpClient.Do(httpReq)
			if err != nil {
				lastErr = err
				log.Printf("[Antigravity] Network error (%s, %s): %v. Trying next model on same account...", acc.Email, model, err)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				errBytes, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				errStr := string(errBytes)
				lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, errStr)
				log.Printf("[Antigravity] Model %s failed on %s: %v", model, acc.Email, lastErr)

				if isAuthError(resp.StatusCode, errStr) {
					log.Printf("[Antigravity] ⚠️ Auth token rejected on account %s. Putting on 15m cooldown and changing account...", acc.Email)
					acc.PutOnCooldown("auth", 15*time.Minute)
					break // Token is broken, skip remaining models on this account
				}

				if isModelCapacityError(resp.StatusCode, errStr) {
					log.Printf("[Antigravity] ⚠️ Model %s capacity exhausted on account %s (503). Retrying with other models (3.7 / 3.6 / claude) on SAME account...", model, acc.Email)
					continue // Seamlessly try next fallback model on THIS account!
				}

				if isQuotaOrRateLimitError(resp.StatusCode, errStr) {
					log.Printf("[Antigravity] ⚠️ Quota ended or rate limit for %s on account %s. Trying fallback model on SAME account...", model, acc.Email)
					continue // Seamlessly try next fallback model on THIS account!
				}

				if isSafetyBlockedError(resp.StatusCode, errStr) {
					log.Printf("[Antigravity] 🛡️ Model %s triggered safety block on %s. Trying next model on SAME account...", model, acc.Email)
					continue
				}

				continue
			}

			// Parse SSE
			var fullText strings.Builder
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "data: ") {
					dataStr := strings.TrimPrefix(line, "data: ")
					if strings.TrimSpace(dataStr) == "[DONE]" {
						break
					}

					var chunk map[string]any
					if err := json.Unmarshal([]byte(dataStr), &chunk); err == nil {
						respObj := chunk
						if inner, ok := chunk["response"].(map[string]any); ok {
							respObj = inner
						}
						if candidates, ok := respObj["candidates"].([]any); ok && len(candidates) > 0 {
							if cand, ok := candidates[0].(map[string]any); ok {
								if finishReason, ok := cand["finishReason"].(string); ok && strings.ToUpper(finishReason) == "SAFETY" {
									log.Printf("[Antigravity] 🛡️ Candidate finishReason=SAFETY on %s. Failing over to next model...", model)
									lastErr = fmt.Errorf("safety blocked on %s", model)
									break
								}
								if content, ok := cand["content"].(map[string]any); ok {
									if parts, ok := content["parts"].([]any); ok {
										for _, p := range parts {
											if pMap, ok := p.(map[string]any); ok {
												if t, ok := pMap["text"].(string); ok {
													fullText.WriteString(t)
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
			resp.Body.Close()

			resStr := fullText.String()
			if strings.TrimSpace(resStr) != "" {
				accountSuccess = true
				return &domain.ChatMessage{
					Role:    "assistant",
					Content: resStr,
				}, model, nil
			}

			lastErr = errors.New("empty text response from stream")
		}

		if !accountSuccess {
			log.Printf("[Antigravity] ⚠️ All fallback models exhausted on account %s. Marking on 3m cooldown and seamlessly changing to next account (1.json / 2.json)...", acc.Email)
			acc.PutOnCooldown("gemini", 3*time.Minute)
		}
	}

	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", errors.New("all antigravity accounts and models failed")
}

// GenerateImage generates an image using Antigravity Imagen 3 with multi-account rotation and failover.
func (c *Client) GenerateImage(ctx context.Context, prompt string) ([]byte, error) {
	return c.GenerateImageWithReference(ctx, prompt, nil, "")
}

// GenerateImageWithReference generates an image using Antigravity Imagen 3 with an optional reference image.
func (c *Client) GenerateImageWithReference(ctx context.Context, prompt string, refImageBytes []byte, refMime string) ([]byte, error) {
	accounts := c.getAccountsForTask("image")
	if len(accounts) == 0 {
		return nil, errors.New("no antigravity accounts configured")
	}

	var lastErr error

	for accIdx, acc := range accounts {
		token, err := c.getAccessTokenFor(ctx, acc)
		if err != nil {
			lastErr = err
			acc.PutOnCooldown("auth", 15*time.Minute)
			continue
		}

		projectID := c.getProjectIDFor(ctx, acc, token)
		log.Printf("[Antigravity] Generating image via %s (account %s %d/%d, hasRef=%v)...", ModelImagen3, acc.Email, accIdx+1, len(accounts), len(refImageBytes) > 0)

		var parts []map[string]any
		if len(refImageBytes) > 0 {
			mime := refMime
			if mime == "" {
				mime = "image/png"
			}
			parts = append(parts, map[string]any{
				"inlineData": map[string]any{
					"mimeType": mime,
					"data":     base64.StdEncoding.EncodeToString(refImageBytes),
				},
			})
		}
		parts = append(parts, map[string]any{
			"text": prompt,
		})

		reqBody := map[string]any{
			"project":     projectID,
			"model":       ModelImagen3,
			"requestType": "image_gen",
			"userAgent":   "antigravity",
			"requestId":   fmt.Sprintf("req-img-%d", time.Now().UnixMilli()),
			"request": map[string]any{
				"contents": []map[string]any{
					{
						"role":  "user",
						"parts": parts,
					},
				},
				"generationConfig": map[string]any{
					"imageConfig": map[string]any{
						"aspectRatio": "1:1",
					},
				},
			},
		}
		jsonBytes, _ := json.Marshal(reqBody)

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURLDaily+":generateContent", bytes.NewReader(jsonBytes))
		if err != nil {
			lastErr = err
			continue
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("User-Agent", c.userAgent)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-client-name", "antigravity")
		httpReq.Header.Set("x-client-version", "4.3.0")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errStr := string(respBytes)
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, errStr)
			if isQuotaOrRateLimitError(resp.StatusCode, errStr) {
				log.Printf("[Antigravity] ⚠️ Imagen 3 quota ended or rate limited on %s. Putting on cooldown for 5m. Seamlessly failing over to next account...", acc.Email)
				acc.PutOnCooldown("image", 5*time.Minute)
			} else if isAuthError(resp.StatusCode, errStr) {
				acc.PutOnCooldown("auth", 15*time.Minute)
			}
			continue
		}

		log.Printf("[Antigravity] Response %d: %.300s", resp.StatusCode, string(respBytes))
		var resObj map[string]any
		if err := json.Unmarshal(respBytes, &resObj); err == nil {
			if respMap, ok := resObj["response"].(map[string]any); ok {
				resObj = respMap
			}

			if candidates, ok := resObj["candidates"].([]any); ok && len(candidates) > 0 {
				if cand, ok := candidates[0].(map[string]any); ok {
					if content, ok := cand["content"].(map[string]any); ok {
						if parts, ok := content["parts"].([]any); ok {
							for _, p := range parts {
								if pMap, ok := p.(map[string]any); ok {
									if inline, ok := pMap["inlineData"].(map[string]any); ok {
										if b64Data, ok := inline["data"].(string); ok && b64Data != "" {
											imgBytes, err := base64.StdEncoding.DecodeString(b64Data)
											if err == nil && len(imgBytes) > 0 {
												log.Printf("[Antigravity] Image generated successfully via %s (Account: %s)!", ModelImagen3, acc.Email)
												return imgBytes, nil
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
		lastErr = errors.New("no image data in response")
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("all accounts failed for image generation")
}
