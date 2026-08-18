package anyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestAnyAPISubscribe(t *testing.T) {
	email := "sumitmehta396@gmail.com"
	password := "Sumit3972@gmail"

	client := NewClient(email, password)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.Subscribe(ctx, "developer")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	if !result.Success {
		t.Fatalf("Expected success=true, got false")
	}

	if result.Subscription == nil {
		t.Fatalf("Expected subscription details, got nil")
	}

	if result.Subscription.PlanCode != "developer" {
		t.Errorf("Expected planCode developer, got %s", result.Subscription.PlanCode)
	}

	t.Logf("Subscription successfully created/renewed: %+v", result.Subscription)
	t.Logf("User: %+v", result.User)
	t.Logf("AccessToken: %s", result.AccessToken)
}

func TestAnyAPIKeyEndpoints(t *testing.T) {
	email := "sumitmehta396@gmail.com"
	password := "Sumit3972@gmail"

	client := NewClient(email, password)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := client.EnsureAuthenticated(ctx, false)
	if err != nil {
		t.Fatalf("Auth failed: %v", err)
	}

	t.Logf("User ID: %s, AccessToken: %s", sess.User.ID, sess.AccessToken)

	// User payload format
	payload, _ := json.Marshal(map[string]any{
		"user_id":   sess.User.ID,
		"models":    []string{"all-team-models"},
		"key_type":  "default",
		"team_id":   sess.User.ID,
		"key_alias": "TelegramBotKey",
	})

	authURL, _ := url.Parse("https://auth.anyapi.ai")
	dashURL, _ := url.Parse("https://dash.anyapi.ai")

	authCookies := client.httpClient.Jar.Cookies(authURL)
	dashCookies := client.httpClient.Jar.Cookies(dashURL)
	t.Logf("Auth cookies count: %d, Dash cookies count: %d", len(authCookies), len(dashCookies))
	for _, c := range authCookies {
		t.Logf("Auth cookie: %s=%s (Domain=%s, Path=%s)", c.Name, c.Value, c.Domain, c.Path)
	}
	for _, c := range dashCookies {
		t.Logf("Dash cookie: %s=%s (Domain=%s, Path=%s)", c.Name, c.Value, c.Domain, c.Path)
	}

	// Make sure all auth cookies are also set on dashURL
	client.httpClient.Jar.SetCookies(dashURL, authCookies)

	var cookieStr string
	for _, c := range authCookies {
		cookieStr += fmt.Sprintf("%s=%s; ", c.Name, c.Value)
	}
	cookieStr += fmt.Sprintf("token=%s; ", sess.AccessToken)

	testCases := []struct {
		name       string
		setHeaders func(req *http.Request)
	}{
		{
			name: "Explicit Cookie Header + Bearer",
			setHeaders: func(req *http.Request) {
				req.Header.Set("Cookie", cookieStr)
				req.Header.Set("Authorization", "Bearer "+sess.AccessToken)
			},
		},
		{
			name: "Explicit Cookie Header + X-API-Key",
			setHeaders: func(req *http.Request) {
				req.Header.Set("Cookie", cookieStr)
				req.Header.Set("x-api-key", sess.AccessToken)
				req.Header.Set("Authorization", "Bearer "+sess.AccessToken)
			},
		},
	}

	for _, tc := range testCases {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://dash.anyapi.ai/api/key/generate", bytes.NewBuffer(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", "https://dash.anyapi.ai")
		req.Header.Set("Referer", "https://dash.anyapi.ai/?page=api-keys")
		tc.setHeaders(req)

		resp, err := client.httpClient.Do(req)
		if err != nil {
			t.Logf("[%s] POST error: %v", tc.name, err)
			continue
		}
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("[%s] POST /api/key/generate -> HTTP %d: %s", tc.name, resp.StatusCode, string(bodyBytes))
	}
}




