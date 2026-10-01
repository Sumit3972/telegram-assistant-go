package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"telegram-ai-assistant/internal/config"
)

func TestHealthHandlerJSON(t *testing.T) {
	cfg := &config.Config{}
	handlers := NewHandlers(cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/health?iterations=1000", nil)
	w := httptest.NewRecorder()

	handlers.Health(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected application/json content-type, got %s", contentType)
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if data["status"] != "ok" {
		t.Errorf("expected status ok, got %v", data["status"])
	}

	comp, ok := data["computation"].(map[string]any)
	if !ok {
		t.Fatalf("expected computation map in response")
	}
	if comp["iterations"] != float64(1000) {
		t.Errorf("expected 1000 iterations, got %v", comp["iterations"])
	}
}

func TestHealthHandlerPlainText(t *testing.T) {
	cfg := &config.Config{}
	handlers := NewHandlers(cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/health?format=text&iterations=500", nil)
	w := httptest.NewRecorder()

	handlers.Health(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Fatalf("expected text/plain content-type, got %s", contentType)
	}
}

func TestPingHandler(t *testing.T) {
	cfg := &config.Config{}
	handlers := NewHandlers(cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()

	handlers.Ping(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}
