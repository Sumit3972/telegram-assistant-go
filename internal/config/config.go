package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config represents all application configuration parameters loaded from environment.
type Config struct {
	Port                  int
	DatabaseURL           string
	TelegramBotToken      string
	TelegramAPIID         int
	TelegramAPIHash       string
	TelegramSessionString string
	MyPersonalName        string
	MyPersonalUsername    string
	MyPersonalUserID      string
	AIBaseURL             string
	AIAPIKey              string
	AIModel               string
	ImageAPIKey           string
	GeminiImageAPIURL     string
	ImageModel            string
	ModelScopeAPIKey      string
	ModelScopeAPIURL      string
	ModelScopeModel       string
	FishAudioAPIKey       string
	FishAudioRefID        string
	MusicBotURL           string
	MusicBotSecret        string
	AnyAPICookie          string
	AnyAPIEmail           string
	AnyAPIPassword        string
	TavilyAPIKey          string
	AntigravityRefreshToken string
	AntigravityEmail        string
	AntigravityAccounts     string
	CodivAPIKey           string

	WorkerPoolSize           int
	DebounceDurationMs       int
	ChatHistoryRetentionDays int
	ChatHistoryMaxPerChat    int
}

// Load reads the .env file (if present) and environment variables into Config.
func Load() (*Config, error) {
	// Try loading from .env, ignore error if file not found (will read system env)
	_ = godotenv.Load(".env")

	portStr := getEnv("PORT", "3000")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 3000
	}

	apiIDStr := getEnv("TELEGRAM_API_ID", "0")
	apiID, _ := strconv.Atoi(apiIDStr)

	workerPoolSizeStr := getEnv("WORKER_POOL_SIZE", "16")
	workerPoolSize, _ := strconv.Atoi(workerPoolSizeStr)
	if workerPoolSize <= 0 {
		workerPoolSize = 16
	}

	debounceMsStr := getEnv("DEBOUNCE_DURATION_MS", "5500")
	debounceMs, _ := strconv.Atoi(debounceMsStr)
	if debounceMs <= 0 {
		debounceMs = 5500
	}

	retentionDaysStr := getEnv("CHAT_HISTORY_RETENTION_DAYS", "7")
	retentionDays, _ := strconv.Atoi(retentionDaysStr)
	if retentionDays <= 0 {
		retentionDays = 7
	}

	maxPerChatStr := getEnv("CHAT_HISTORY_MAX_PER_CHAT", "100")
	maxPerChat, _ := strconv.Atoi(maxPerChatStr)
	if maxPerChat <= 0 {
		maxPerChat = 100
	}

	cfg := &Config{
		Port:                  port,
		DatabaseURL:           getEnv("DATABASE_URL", ""),
		TelegramBotToken:      getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramAPIID:         apiID,
		TelegramAPIHash:       getEnv("TELEGRAM_API_HASH", ""),
		TelegramSessionString: getEnv("TELEGRAM_SESSION_STRING", ""),
		MyPersonalName:        getEnv("MY_PERSONAL_NAME", "Chavi Sharma"),
		MyPersonalUsername:    strings.TrimPrefix(getEnv("MY_PERSONAL_USERNAME", "Chavi396"), "@"),
		MyPersonalUserID:      getEnv("MY_PERSONAL_USER_ID", "8542441463"),
		AIBaseURL:             getEnv("AI_BASE_URL", "https://run.forgeapi.org/v1"),
		AIAPIKey:              getEnv("AI_API_KEY", "fg-live-NDg5MTE2YjgtNmJlMC00Njk2LWEwNDItNjc5ZDFlZDQzYjJlfHN1bWl0bWVodGEzOTZAZ21haWwuY29tfDE3ODcwMjUyNzExNDZ8MA.8afc4bf3621a1317b874"),
		AIModel:               getEnv("AI_MODEL", "deepseek/deepseek-v4-flash"),
		ImageAPIKey:           getEnv("IMAGE_API_KEY", getEnv("AI_API_KEY", "sk-ukfBCvlCRKRyS1xr5nRv3bqSuDudVy5mQf3OuW13QVF6q2V8")),
		GeminiImageAPIURL:     getEnv("GEMINI_IMAGE_API_URL", "https://api.futureppo.top/v1/images/generations"),
		ImageModel:            getEnv("IMAGE_MODEL", "agnes-image-2.5-flash"),
		ModelScopeAPIKey:      getEnv("MODELSCOPE_API_KEY", "ms-b651c584-6b06-482f-90c7-b8fe50fd516c"),
		ModelScopeAPIURL:      getEnv("MODELSCOPE_API_BASE", "https://api-inference.modelscope.ai/v1"),
		ModelScopeModel:       getEnv("MODELSCOPE_IMAGE_MODEL", "Qwen/Qwen-Image-2.1"),
		FishAudioAPIKey:       getEnv("FISH_AUDIO_API_KEY", "sk-fish-DpjR6d_vg1G18CpBL13vXa_FXHL5qhOfgq_xcSzcgRM"),
		FishAudioRefID:        getEnv("FISH_AUDIO_REFERENCE_ID", "ac315c50a9734bb7a608102fce24e601"),
		MusicBotURL:           getEnv("MUSIC_BOT_URL", "https://sumitmehta396-hinata-music-bot.hf.space"),
		MusicBotSecret:        getEnv("MUSIC_BOT_SECRET", "HinataSecretMusicKey2026"),
		AnyAPICookie:          getEnv("ANYAPI_COOKIE", ""),
		AnyAPIEmail:           getEnv("ANYAPI_EMAIL", "sumitmehta396@gmail.com"),
		AnyAPIPassword:        getEnv("ANYAPI_PASSWORD", "Sumit3972@gmail"),
		TavilyAPIKey:          getEnv("TAVILY_API_KEY", "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"),
		AntigravityRefreshToken: getEnv("ANTIGRAVITY_REFRESH_TOKEN", ""),
		AntigravityEmail:        getEnv("ANTIGRAVITY_EMAIL", ""),
		AntigravityAccounts:     getEnv("ANTIGRAVITY_ACCOUNTS", ""),
		CodivAPIKey:           getEnv("CODIV_API_KEY", "sk-codiv-wXAmZjRSnKWYunGwYlTkZxhnxDSLyiPfVAzHnpeh"),

		WorkerPoolSize:           workerPoolSize,
		DebounceDurationMs:       debounceMs,
		ChatHistoryRetentionDays: retentionDays,
		ChatHistoryMaxPerChat:    maxPerChat,
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required in environment")
	}
	if cfg.TelegramBotToken == "" {
		return nil, fmt.Errorf("TELEGRAM_BOT_TOKEN is required in environment")
	}

	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}
