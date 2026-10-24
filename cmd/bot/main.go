package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"telegram-ai-assistant/internal/ai"
	"telegram-ai-assistant/internal/ai/antigravity"
	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/database"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/moderator"
	"telegram-ai-assistant/internal/server"
	"telegram-ai-assistant/internal/telegram"
	"telegram-ai-assistant/internal/userbot"
)

var defaultAnyApiKeys = []string{
	"sk-gynEZUNUo5UI4Y2dUiFZpNnh6w04zDZeu8m0t6oxMr4MHUYh",
	"sk-1p13iZ5fw3RMfwVjUZbE1g",
	"sk-po1L8m4k_z-3lH__ImrrFQ",
	"sk-wBP1XHNGZy_-H26BFohzLw",
	"sk-RZc76KfExxqciFCSwkBEtQ",
	"sk-Fly5DoiUPC50fi87jGQVlw",
	"sk-8UHTlLgAQ-S-zsbMnM78Og",
}

func main() {
	log.Println("🌟 Starting Telegram AI Admin Assistant (Go Edition)...")

	// 1. Load Configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Configuration error: %v", err)
	}

	// 2. Connect to PostgreSQL Pool
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dbPool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("❌ Failed to connect to PostgreSQL: %v", err)
	}
	defer dbPool.Close()
	log.Println("✅ PostgreSQL connection pool established.")

	// 3. Run Auto-Migrations
	if err := database.AutoMigrate(ctx, dbPool); err != nil {
		log.Fatalf("❌ Database migration failed: %v", err)
	}

	// 4. Initialize Repositories
	groupRepo := database.NewGroupRepository(dbPool)
	adminRepo := database.NewAdminRepository(dbPool)
	warningRepo := database.NewWarningRepository(dbPool)
	modLogRepo := database.NewModerationLogRepository(dbPool)
	historyRepo := database.NewHistoryRepository(dbPool)
	karmaRepo := database.NewKarmaRepository(dbPool)
	captchaRepo := database.NewCaptchaRepository(dbPool)
	mentionRepo := database.NewMentionRepository(dbPool)
	relRepo := database.NewRelationshipRepository(dbPool)
	shipRepo := database.NewShipRepository(dbPool)
	apiKeyRepo := database.NewApiKeyRepository(dbPool)
	perfRepo := database.NewPerformanceRepository(dbPool)
	blockedRepo := database.NewBlockedUserRepository(dbPool)
	abuseRepo := database.NewAbuseRecordRepository(dbPool)

	// Seed API Keys
	if err := apiKeyRepo.InitSchemaAndSeed(ctx, defaultAnyApiKeys, cfg.AIAPIKey); err != nil {
		log.Printf("⚠️ API key seeding warning: %v", err)
	}

	// 5. Initialize Native Google Antigravity Client (Gemini 3.8 Flash, Claude 4.6, Imagen 3)
	agyClient := antigravity.NewClient(antigravity.Config{
		AccountsJSON:  cfg.AntigravityAccounts,
		RefreshToken:  cfg.AntigravityRefreshToken,
		Email:         cfg.AntigravityEmail,
		AccountsPath:  "accounts.json",
		AccountsFiles: []string{"1.json", "2.json"},
	})

	log.Printf("🚀 [Antigravity Engine] Initialized with %d account(s) ready for Gemini 3.8, Claude 4.6 & Imagen 3", agyClient.AccountCount())

	// Asynchronously inspect and log live quotas for all accounts
	go func() {
		qCtx, qCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer qCancel()
		agyClient.SyncAllQuotas(qCtx)
	}()

	providers := []ai.ProviderConfig{
		{
			BaseURL: cfg.AIBaseURL,
			APIKey:  cfg.AIAPIKey,
			Models: []string{
				"deepseek/deepseek-chat",
				"deepseek-r1",
				"deepseek-v3.2",
			},
		},
	}

	aiClient := ai.NewClient(ai.ClientConfig{
		Antigravity: agyClient,
		Providers:   providers,
		PerfRepo:    perfRepo,
	})

	// 6. Initialize Telegram Bot API Client
	botClient := telegram.NewBotClient(cfg.TelegramBotToken)

	// 7. Initialize Moderator
	mod := moderator.NewModerator(
		cfg,
		groupRepo,
		adminRepo,
		warningRepo,
		modLogRepo,
		historyRepo,
		karmaRepo,
		captchaRepo,
		mentionRepo,
		relRepo,
		shipRepo,
		apiKeyRepo,
		perfRepo,
		blockedRepo,
		abuseRepo,
		botClient,
		aiClient,
	)

	// 8. Initialize Worker Pool
	workerPool := server.NewWorkerPool(cfg.WorkerPoolSize, 500, mod)

	// 9. Initialize and Start Userbot Manager
	userbotMgr := userbot.NewUserbotManager(cfg, botClient, func(ctx context.Context, update *domain.TelegramUpdate) {
		workerPool.Enqueue(update)
	})
	mod.SetUserbotSender(userbotMgr)
	if err := userbotMgr.Start(context.Background()); err != nil {
		log.Printf("⚠️ Userbot startup warning: %v", err)
	}

	// 10. Start HTTP Server
	srv := server.NewServer(cfg, botClient, workerPool, dbPool)

	go func() {
		log.Printf("🚀 HTTP Server listening on port %d...", cfg.Port)
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("❌ HTTP server fatal error: %v", err)
		}
	}()

	// 11. Start Periodic Database Cleaner (Protects Neon DB quota by pruning stale history & sessions)
	cleanerStop := make(chan struct{})
	go func() {
		retentionDays := cfg.ChatHistoryRetentionDays
		if retentionDays <= 0 {
			retentionDays = 7
		}
		maxPerChat := cfg.ChatHistoryMaxPerChat
		if maxPerChat <= 0 {
			maxPerChat = 100
		}
		maxAge := time.Duration(retentionDays) * 24 * time.Hour

		// Run initial cleanup 15s after boot
		select {
		case <-time.After(15 * time.Second):
			if err := historyRepo.CleanOldHistory(context.Background(), maxAge, maxPerChat); err != nil {
				log.Printf("⚠️ Initial chat history cleanup error: %v", err)
			}
		case <-cleanerStop:
			return
		}

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := historyRepo.CleanOldHistory(context.Background(), maxAge, maxPerChat); err != nil {
					log.Printf("⚠️ Periodic chat history cleanup error: %v", err)
				}
			case <-cleanerStop:
				return
			}
		}
	}()

	// 12. Graceful Shutdown on SIGINT / SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down gracefully...")
	close(cleanerStop)
	_ = srv.Stop(10 * time.Second)
	log.Println("👋 Server stopped. Bye!")
}
