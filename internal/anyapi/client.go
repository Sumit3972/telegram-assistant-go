package anyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"
)

// SubscriptionDetails represents the subscription model returned by AnyAPI
type SubscriptionDetails struct {
	LagoID             string  `json:"lagoId"`
	ExternalID         string  `json:"externalId"`
	PlanCode           string  `json:"planCode"`
	PlanName           string  `json:"planName"`
	Status             string  `json:"status"`
	BillingTime        string  `json:"billingTime"`
	StartedAt          string  `json:"startedAt"`
	CurrentPeriodStart string  `json:"currentPeriodStart"`
	CurrentPeriodEnd   string  `json:"currentPeriodEnd"`
	NextPlanCode       *string `json:"nextPlanCode"`
	DowngradePlanDate  *string `json:"downgradePlanDate"`
}

// SubscriptionResponse is the top-level response payload
type SubscriptionResponse struct {
	Subscription *SubscriptionDetails `json:"subscription"`
	Error        *string              `json:"error,omitempty"`
}

// UserProfile represents the logged in user profile
type UserProfile struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	FirstName     string `json:"firstName"`
	LastName      string `json:"lastName"`
	EmailVerified bool   `json:"emailVerified"`
	UserRole      string `json:"userRole"`
	PremiumUser   bool   `json:"premiumUser"`
}

// DashboardSession represents the /auth/session response
type DashboardSession struct {
	IsAuthenticated bool         `json:"isAuthenticated"`
	User            *UserProfile `json:"user"`
	AuthMethod      string       `json:"authMethod"`
	AccessToken     string       `json:"accessToken"`
	Error           any          `json:"error"`
}

// SubscribeResult is returned to the caller on success
type SubscribeResult struct {
	Success      bool                 `json:"success"`
	Message      string               `json:"message"`
	Attempts     int                  `json:"attempts"`
	PlanCode     string               `json:"planCode"`
	Subscription *SubscriptionDetails `json:"subscription"`
	AccessToken  string               `json:"accessToken,omitempty"`
	User         *UserProfile         `json:"user,omitempty"`
}

type flowInitResponse struct {
	ID string `json:"id"`
	UI struct {
		Nodes []struct {
			Attributes struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"attributes"`
		} `json:"nodes"`
	} `json:"ui"`
}

// Client manages automated authentication and billing interactions with AnyAPI
type Client struct {
	email      string
	password   string
	httpClient *http.Client
	session    *DashboardSession
	mu         sync.RWMutex
}

// NewClient creates a new AnyAPI client with email & password credentials
func NewClient(email, password string) *Client {
	return &Client{
		email:    email,
		password: password,
	}
}

// EnsureAuthenticated checks or executes dynamic login to refresh cookies and tokens
func (c *Client) EnsureAuthenticated(ctx context.Context, force bool) (*DashboardSession, error) {
	if !force {
		c.mu.RLock()
		if c.session != nil && c.session.IsAuthenticated && c.httpClient != nil {
			sess := c.session
			c.mu.RUnlock()
			return sess, nil
		}
		c.mu.RUnlock()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !force && c.session != nil && c.session.IsAuthenticated && c.httpClient != nil {
		return c.session, nil
	}

	if c.email == "" || c.password == "" {
		return nil, errors.New("AnyAPI email and password must be configured")
	}

	log.Printf("[AnyAPI Auth] Session expired or uninitialized. Logging in as %s...", c.email)

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	httpClient := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
	}

	// Step 1: Initialize browser flow from Ory Kratos
	initReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://auth.anyapi.ai/self-service/login/browser", nil)
	if err != nil {
		return nil, err
	}
	initReq.Header.Set("Accept", "application/json")

	initResp, err := httpClient.Do(initReq)
	if err != nil {
		return nil, fmt.Errorf("auth flow init request failed: %w", err)
	}
	defer initResp.Body.Close()

	if initResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(initResp.Body)
		return nil, fmt.Errorf("auth flow init failed (HTTP %d): %s", initResp.StatusCode, string(body))
	}

	var flow flowInitResponse
	if err := json.NewDecoder(initResp.Body).Decode(&flow); err != nil {
		return nil, fmt.Errorf("failed to decode flow init response: %w", err)
	}

	var csrfToken string
	for _, node := range flow.UI.Nodes {
		if node.Attributes.Name == "csrf_token" {
			csrfToken = node.Attributes.Value
			break
		}
	}

	if csrfToken == "" {
		return nil, errors.New("csrf_token not found in Ory Kratos flow")
	}

	// Step 2: Submit credentials to Ory Kratos
	payload, err := json.Marshal(map[string]string{
		"method":     "password",
		"identifier": c.email,
		"password":   c.password,
		"csrf_token": csrfToken,
	})
	if err != nil {
		return nil, err
	}

	loginURL := fmt.Sprintf("https://auth.anyapi.ai/self-service/login?flow=%s", flow.ID)
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Accept", "application/json")
	loginReq.Header.Set("Origin", "https://dash.anyapi.ai")
	loginReq.Header.Set("Referer", "https://dash.anyapi.ai/")

	loginResp, err := httpClient.Do(loginReq)
	if err != nil {
		return nil, fmt.Errorf("auth submission request failed: %w", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(loginResp.Body)
		return nil, fmt.Errorf("credentials rejected (HTTP %d): %s", loginResp.StatusCode, string(body))
	}

	// Step 3: Fetch dashboard session and access token
	sessionReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://dash.anyapi.ai/auth/session", nil)
	if err != nil {
		return nil, err
	}
	sessionReq.Header.Set("Accept", "application/json")
	sessionReq.Header.Set("Referer", "https://dash.anyapi.ai/?page=any-chat")

	sessionResp, err := httpClient.Do(sessionReq)
	if err != nil {
		return nil, fmt.Errorf("session verification failed: %w", err)
	}
	defer sessionResp.Body.Close()

	if sessionResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sessionResp.Body)
		return nil, fmt.Errorf("session endpoint returned HTTP %d: %s", sessionResp.StatusCode, string(body))
	}

	var session DashboardSession
	if err := json.NewDecoder(sessionResp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("failed to parse dashboard session: %w", err)
	}

	if !session.IsAuthenticated {
		return nil, errors.New("authentication succeeded but dashboard session reports isAuthenticated=false")
	}

	// Sync token into cookies
	dashURL, _ := url.Parse("https://dash.anyapi.ai")
	if session.AccessToken != "" {
		httpClient.Jar.SetCookies(dashURL, []*http.Cookie{
			{
				Name:  "token",
				Value: session.AccessToken,
				Path:  "/",
			},
		})
	}

	c.httpClient = httpClient
	c.session = &session

	log.Printf("✅ [AnyAPI Auth] Successfully authenticated as %s (Token: %s)", c.email, session.AccessToken)
	return &session, nil
}

// InvalidateSession clears the cached session and client to force re-login on next attempt
func (c *Client) InvalidateSession() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = nil
	c.httpClient = nil
}

// Subscribe attempts to activate or renew the subscription with up to maxRetries (5)
func (c *Client) Subscribe(ctx context.Context, planCode string) (*SubscribeResult, error) {
	if planCode == "" {
		planCode = "developer"
	}

	const maxRetries = 5
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("[AnyAPI Subscription] Attempt %d/%d for plan: %s", attempt, maxRetries, planCode)

		// 1. Ensure authenticated session
		session, err := c.EnsureAuthenticated(ctx, false)
		if err != nil {
			log.Printf("⚠️ [AnyAPI Subscription] Attempt %d auth error: %v", attempt, err)
			lastErr = err
			c.InvalidateSession()
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		// 2. Prepare subscription POST request
		payloadBytes, _ := json.Marshal(map[string]string{
			"planCode": planCode,
		})

		subReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://dash.anyapi.ai/api/billing/subscription", bytes.NewBuffer(payloadBytes))
		if err != nil {
			return nil, err
		}
		subReq.Header.Set("Content-Type", "application/json")
		subReq.Header.Set("Accept", "application/json, text/plain, */*")
		subReq.Header.Set("Origin", "https://dash.anyapi.ai")
		subReq.Header.Set("Referer", "https://dash.anyapi.ai/?page=billing")
		subReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36")

		c.mu.RLock()
		httpClient := c.httpClient
		c.mu.RUnlock()

		if httpClient == nil {
			c.InvalidateSession()
			continue
		}

		resp, err := httpClient.Do(subReq)
		if err != nil {
			log.Printf("⚠️ [AnyAPI Subscription] Attempt %d network error: %v", attempt, err)
			lastErr = err
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		// Check if session expired or unauthorized (401, 403)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			log.Printf("⚠️ [AnyAPI Subscription] Attempt %d session expired (HTTP %d). Invalidating session and re-logging in...", attempt, resp.StatusCode)
			lastErr = fmt.Errorf("session expired (HTTP %d)", resp.StatusCode)
			c.InvalidateSession()
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("⚠️ [AnyAPI Subscription] Attempt %d returned HTTP %d: %s", attempt, resp.StatusCode, string(respBody))
			lastErr = fmt.Errorf("subscription API returned HTTP %d: %s", resp.StatusCode, string(respBody))
			// If 5xx error or rate limit, wait and retry
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			continue
		}

		// Parse subscription response
		var subResp SubscriptionResponse
		if err := json.Unmarshal(respBody, &subResp); err != nil {
			log.Printf("⚠️ [AnyAPI Subscription] Attempt %d failed to parse JSON: %v", attempt, err)
			lastErr = fmt.Errorf("failed to parse response JSON: %w", err)
			continue
		}

		if subResp.Subscription == nil {
			log.Printf("⚠️ [AnyAPI Subscription] Attempt %d returned missing subscription field: %s", attempt, string(respBody))
			lastErr = fmt.Errorf("invalid subscription response payload: %s", string(respBody))
			continue
		}

		log.Printf("🎉 [AnyAPI Subscription] Successfully activated plan '%s' on attempt %d! (Status: %s, CurrentPeriodEnd: %s)",
			subResp.Subscription.PlanCode, attempt, subResp.Subscription.Status, subResp.Subscription.CurrentPeriodEnd)

		return &SubscribeResult{
			Success:      true,
			Message:      fmt.Sprintf("Subscription for '%s' activated successfully", subResp.Subscription.PlanCode),
			Attempts:     attempt,
			PlanCode:     subResp.Subscription.PlanCode,
			Subscription: subResp.Subscription,
			AccessToken:  session.AccessToken,
			User:         session.User,
		}, nil
	}

	return nil, fmt.Errorf("failed to subscribe to plan '%s' after %d attempts: %w", planCode, maxRetries, lastErr)
}
