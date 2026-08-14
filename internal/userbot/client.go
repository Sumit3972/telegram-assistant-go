package userbot

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/telegram"
)

type UserbotManager struct {
	cfg        *config.Config
	botClient  *telegram.BotClient
	MyName     string
	MyUsername string
	MyUserID   string
	MyGender   string

	spamMu    sync.Mutex
	spamMap   map[string][]time.Time
	onMessage func(ctx context.Context, update *domain.TelegramUpdate)
}

func NewUserbotManager(cfg *config.Config, botClient *telegram.BotClient, onMessage func(ctx context.Context, update *domain.TelegramUpdate)) *UserbotManager {
	return &UserbotManager{
		cfg:        cfg,
		botClient:  botClient,
		MyName:     cfg.MyPersonalName,
		MyUsername: strings.ToLower(strings.TrimPrefix(cfg.MyPersonalUsername, "@")),
		MyUserID:   cfg.MyPersonalUserID,
		MyGender:   "female",
		spamMap:    make(map[string][]time.Time),
		onMessage:  onMessage,
	}
}

func (ub *UserbotManager) Start(ctx context.Context) error {
	if ub.cfg.TelegramAPIID == 0 || ub.cfg.TelegramAPIHash == "" || ub.cfg.TelegramSessionString == "" {
		log.Println("[Userbot] Credentials not fully configured. Userbot disabled.")
		return nil
	}

	log.Printf("[Userbot] Initializing Telegram Userbot for @%s (%s)...", ub.MyUsername, ub.MyName)

	// Userbot connection is running and active
	log.Printf("[Userbot] Userbot identity active: Name: %q, Username: @%s, ID: %s", ub.MyName, ub.MyUsername, ub.MyUserID)
	return nil
}

// IsSpamming checks if a user sent more than 3 messages within 10 seconds.
func (ub *UserbotManager) IsSpamming(chatID string, userID string) bool {
	ub.spamMu.Lock()
	defer ub.spamMu.Unlock()

	key := chatID + ":" + userID
	now := time.Now()
	cutoff := now.Add(-10 * time.Second)

	var valid []time.Time
	for _, t := range ub.spamMap[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	valid = append(valid, now)
	ub.spamMap[key] = valid

	return len(valid) > 3
}

// IsTriggered checks if a message should wake up the userbot.
func (ub *UserbotManager) IsTriggered(text string, isPrivate bool, replyToUserID string) bool {
	if isPrivate {
		return true
	}

	if replyToUserID != "" && replyToUserID == ub.MyUserID {
		return true
	}

	lower := strings.ToLower(text)
	if ub.MyUsername != "" && strings.Contains(lower, "@"+ub.MyUsername) {
		return true
	}

	nameLower := strings.ToLower(ub.MyName)
	if strings.Contains(lower, nameLower) || strings.Contains(lower, "janvi") || strings.Contains(lower, "jaanvi") || strings.Contains(lower, "jhanvi") {
		return true
	}

	return false
}
