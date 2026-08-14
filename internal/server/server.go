package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/telegram"
)

type Server struct {
	cfg        *config.Config
	router     *chi.Mux
	handlers   *Handlers
	workerPool *WorkerPool
	httpServer *http.Server
}

func NewServer(cfg *config.Config, botClient *telegram.BotClient, workerPool *WorkerPool) *Server {
	handlers := NewHandlers(cfg, botClient, workerPool)
	r := chi.NewRouter()

	// Middlewares
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(180 * time.Second))

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Register Routes
	r.Post("/webhook/{token}", handlers.Webhook)
	r.Get("/setup", handlers.Setup)
	r.Get("/media/{fileId}", handlers.MediaProxy)
	r.Get("/", handlers.Health)
	r.Get("/ping", handlers.Ping)
	r.Get("/admin/upgrade-plan", handlers.UpgradePlan)
	r.Post("/admin/upgrade-plan", handlers.UpgradePlan)
	r.Get("/api/billing/subscription", handlers.UpgradePlan)
	r.Post("/api/billing/subscription", handlers.UpgradePlan)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 180 * time.Second,
		IdleTimeout:  240 * time.Second,
	}

	return &Server{
		cfg:        cfg,
		router:     r,
		handlers:   handlers,
		workerPool: workerPool,
		httpServer: httpServer,
	}
}

func (s *Server) Start() error {
	s.workerPool.Start()
	return s.httpServer.ListenAndServe()
}

func (s *Server) Stop(ctx time.Duration) error {
	s.workerPool.Stop()
	return s.httpServer.Close()
}
