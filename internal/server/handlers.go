package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"telegram-ai-assistant/internal/anyapi"
	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/telegram"
)

type Handlers struct {
	cfg          *config.Config
	botClient    *telegram.BotClient
	workerPool   *WorkerPool
	dbPool       *pgxpool.Pool
	httpClient   *http.Client
	anyapiClient *anyapi.Client
	startTime    time.Time
}

func NewHandlers(cfg *config.Config, botClient *telegram.BotClient, workerPool *WorkerPool, dbPool ...*pgxpool.Pool) *Handlers {
	var pool *pgxpool.Pool
	if len(dbPool) > 0 {
		pool = dbPool[0]
	}
	return &Handlers{
		cfg:          cfg,
		botClient:    botClient,
		workerPool:   workerPool,
		dbPool:       pool,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		anyapiClient: anyapi.NewClient(cfg.AnyAPIEmail, cfg.AnyAPIPassword),
		startTime:    time.Now(),
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
	// Parse optional computation iterations (?n=... or ?iterations=..., default 40000, max 200000)
	iterations := 40000
	if q := r.URL.Query().Get("iterations"); q != "" {
		if val, err := strconv.Atoi(q); err == nil && val > 0 {
			iterations = val
		}
	} else if q := r.URL.Query().Get("n"); q != "" {
		if val, err := strconv.Atoi(q); err == nil && val > 0 {
			iterations = val
		}
	}
	if iterations > 200000 {
		iterations = 200000
	}

	// 1. Perform CPU computation (hash chaining to ensure CPU core wake-up)
	hashHex, compDuration := performCPUWorkload(iterations)

	// 2. Perform DB Ping if pool is configured (wakes up database connection and remote serverless Neon DB)
	var dbInfo map[string]any
	if h.dbPool != nil {
		pingCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		dbStart := time.Now()
		if err := h.dbPool.Ping(pingCtx); err != nil {
			dbInfo = map[string]any{
				"status":     "error",
				"error":      err.Error(),
				"latency_ms": float64(time.Since(dbStart).Microseconds()) / 1000.0,
			}
		} else {
			dbInfo = map[string]any{
				"status":     "connected",
				"latency_ms": float64(time.Since(dbStart).Microseconds()) / 1000.0,
			}
		}
	} else {
		dbInfo = map[string]any{
			"status": "not_configured",
		}
	}

	// 3. Runtime system memory & goroutines
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	uptime := time.Since(h.startTime)

	// Support explicit plain text format if requested
	if r.URL.Query().Get("format") == "text" || (strings.HasPrefix(r.Header.Get("Accept"), "text/plain") && !strings.Contains(r.Header.Get("Accept"), "application/json")) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "OK: Server is awake! Computation: %d iterations in %.2fms | Uptime: %s\n",
			iterations, float64(compDuration.Microseconds())/1000.0, uptime.Truncate(time.Second))
		return
	}

	response := map[string]any{
		"status":         "ok",
		"message":        "Server is awake and active",
		"service":        "Telegram AI Assistant",
		"uptime":         uptime.Truncate(time.Second).String(),
		"uptime_seconds": int64(uptime.Seconds()),
		"computation": map[string]any{
			"iterations":  iterations,
			"duration_ms": float64(compDuration.Microseconds()) / 1000.0,
			"result_hash": hashHex,
		},
		"database": dbInfo,
		"system": map[string]any{
			"goroutines": runtime.NumGoroutine(),
			"alloc_mb":   fmt.Sprintf("%.2f MB", float64(m.Alloc)/(1024*1024)),
			"sys_mb":     fmt.Sprintf("%.2f MB", float64(m.Sys)/(1024*1024)),
			"num_gc":     m.NumGC,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *Handlers) Wakeup(w http.ResponseWriter, r *http.Request) {
	h.Health(w, r)
}

func performCPUWorkload(iterations int) (string, time.Duration) {
	start := time.Now()
	data := []byte(fmt.Sprintf("wakeup-%d-%d", start.UnixNano(), iterations))
	h := sha256.New()
	for i := 0; i < iterations; i++ {
		h.Reset()
		h.Write(data)
		data = h.Sum(nil)
	}
	return hex.EncodeToString(data), time.Since(start)
}

func (h *Handlers) Ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"message":   "Server is awake",
		"uptime":    time.Since(h.startTime).Truncate(time.Second).String(),
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
