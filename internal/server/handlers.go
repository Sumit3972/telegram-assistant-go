package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"telegram-ai-assistant/internal/anyapi"
	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/telegram"
)

type Handlers struct {
	cfg          *config.Config
	botClient    *telegram.BotClient
	workerPool   *WorkerPool
	httpClient   *http.Client
	anyapiClient *anyapi.Client
}

func NewHandlers(cfg *config.Config, botClient *telegram.BotClient, workerPool *WorkerPool) *Handlers {
	return &Handlers{
		cfg:          cfg,
		botClient:    botClient,
		workerPool:   workerPool,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		anyapiClient: anyapi.NewClient(cfg.AnyAPIEmail, cfg.AnyAPIPassword),
	}
}

func (h *Handlers) Webhook(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" || token != h.cfg.TelegramBotToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var update domain.TelegramUpdate
	if err := json.Unmarshal(bodyBytes, &update); err != nil {
		log.Printf("[Webhook Error] Failed to parse update: %v", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if update.Message != nil {
		text := update.Message.Text
		if text == "" {
			text = update.Message.Caption
		}
		fromUname := update.Message.From.Username
		if fromUname == "" {
			fromUname = update.Message.From.FirstName
		}
		log.Printf("[Webhook Event] update_id=%d, chatId=%d, from=%s, text=%q",
			update.UpdateID, update.Message.Chat.ID, fromUname, text)
	}

	// Dispatch to worker pool without blocking Telegram webhook
	h.workerPool.Enqueue(&update)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *Handlers) Setup(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	protocol := "https"
	if strings.Contains(host, "localhost") || strings.Contains(host, "127.0.0.1") {
		protocol = "http"
	}
	webhookURL := fmt.Sprintf("%s://%s/webhook/%s", protocol, host, h.cfg.TelegramBotToken)

	allowedUpdates := []string{"message", "callback_query", "chat_member"}
	ok, err := h.botClient.SetWebhook(r.Context(), webhookURL, allowedUpdates)
	if err != nil || !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Failed to set Telegram webhook",
			"error":   fmt.Sprintf("%v", err),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":     true,
		"message":     "Telegram webhook configured successfully!",
		"webhook_url": webhookURL,
	})
}

func (h *Handlers) MediaProxy(w http.ResponseWriter, r *http.Request) {
	fileID := chi.URLParam(r, "fileId")
	if fileID == "" {
		http.Error(w, "Missing fileId", http.StatusBadRequest)
		return
	}

	tgFile, err := h.botClient.GetFile(r.Context(), fileID)
	if err != nil || tgFile == nil || tgFile.FilePath == "" {
		http.Error(w, "File not found on Telegram", http.StatusNotFound)
		return
	}

	fileURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", h.cfg.TelegramBotToken, tgFile.FilePath)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, fileURL, nil)
	if err != nil {
		http.Error(w, "Failed to create media request", http.StatusInternalServerError)
		return
	}

	resp, err := h.httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to fetch media from Telegram servers", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, resp.Body)
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("🤖 Telegram AI Admin Assistant is running in Go!"))
}

func (h *Handlers) Ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"message":   "Server is awake",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handlers) UpgradePlan(w http.ResponseWriter, r *http.Request) {
	planCode := "developer"

	// 1. Check query parameter e.g. ?plan=developer or ?planCode=developer
	if qPlan := r.URL.Query().Get("plan"); qPlan != "" {
		planCode = qPlan
	} else if qPlanCode := r.URL.Query().Get("planCode"); qPlanCode != "" {
		planCode = qPlanCode
	}

	// 2. Check JSON request body if present
	if r.Body != nil {
		var body struct {
			PlanCode string `json:"planCode"`
			Plan     string `json:"plan"`
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &body); err == nil {
				if body.PlanCode != "" {
					planCode = body.PlanCode
				} else if body.Plan != "" {
					planCode = body.Plan
				}
			}
		}
	}

	log.Printf("[HTTP Admin] Subscription upgrade requested for plan: %s", planCode)

	// 3. Call AnyAPI Subscribe (with automatic Ory Kratos login and max 5 retries)
	result, err := h.anyapiClient.Subscribe(r.Context(), planCode)
	if err != nil {
		log.Printf("❌ [HTTP Admin] Subscription upgrade failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Failed to subscribe after retries",
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}
