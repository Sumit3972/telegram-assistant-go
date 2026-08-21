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

	// Seed API Keys
	if err := apiKeyRepo.InitSchemaAndSeed(ctx, defaultAnyApiKeys, cfg.AIAPIKey); err != nil {
		log.Printf("⚠️ API key seeding warning: %v", err)
	}

	// 5. Initialize AI Client
	providers := []ai.ProviderConfig{
		{
			BaseURL: "https://novarouter.site/api/v1",
			APIKey:  "nr_sk_JBswU_kp6fKPGtuDpKZGoqUUBags",
			Models: []string{
				"claude-fable-5",
				"claude-opus-5",
				"gpt-5.6-sol",
				"gemini-3-pro",
				"claude-sonnet-5",
			},
		},
		{
			BaseURL: "https://gorouter.app/v1",
			APIKey:  "sk-LlJ8vC0ociQnotHY5gFw3K6onFmXlmFSNUJs8uGOmzLPxqpM",
			Models: []string{
				"claude-opus-4-8",
			},
		},
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
		Providers: providers,
		PerfRepo:  perfRepo,
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
	srv := server.NewServer(cfg, botClient, workerPool)

	go func() {
		log.Printf("🚀 HTTP Server listening on port %d...", cfg.Port)
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("❌ HTTP server fatal error: %v", err)
		}
	}()

	// 11. Graceful Shutdown on SIGINT / SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("🛑 Shutting down gracefully...")
	_ = srv.Stop(10 * time.Second)
	log.Println("👋 Server stopped. Bye!")
}
