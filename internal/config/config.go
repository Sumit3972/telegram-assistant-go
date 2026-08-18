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
	FishAudioAPIKey       string
	MusicBotURL           string
	MusicBotSecret        string
	AnyAPICookie          string
	AnyAPIEmail           string
	AnyAPIPassword        string
	TavilyAPIKey          string
	WorkerPoolSize        int
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
		ImageModel:            getEnv("IMAGE_MODEL", "gpt-image-2"),
		FishAudioAPIKey:       getEnv("FISH_AUDIO_API_KEY", ""),
		MusicBotURL:           getEnv("MUSIC_BOT_URL", "https://sumitmehta396-hinata-music-bot.hf.space"),
		MusicBotSecret:        getEnv("MUSIC_BOT_SECRET", "HinataSecretMusicKey2026"),
		AnyAPICookie:          getEnv("ANYAPI_COOKIE", ""),
		AnyAPIEmail:           getEnv("ANYAPI_EMAIL", "sumitmehta396@gmail.com"),
		AnyAPIPassword:        getEnv("ANYAPI_PASSWORD", "Sumit3972@gmail"),
		TavilyAPIKey:          getEnv("TAVILY_API_KEY", "tvly-dev-dummy"),
		WorkerPoolSize:        workerPoolSize,
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
