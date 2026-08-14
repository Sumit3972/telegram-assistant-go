package userbot

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/domain"
	telegramBot "telegram-ai-assistant/internal/telegram"
)

type UserbotManager struct {
	cfg        *config.Config
	botClient  *telegramBot.BotClient
	MyName     string
	MyUsername string
	MyUserID   string
	MyGender   string

	spamMu    sync.Mutex
	spamMap   map[string][]time.Time
	onMessage func(ctx context.Context, update *domain.TelegramUpdate)
}

func NewUserbotManager(cfg *config.Config, botClient *telegramBot.BotClient, onMessage func(ctx context.Context, update *domain.TelegramUpdate)) *UserbotManager {
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

func parseGramJSSession(raw string) (*session.Data, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "1") {
		return nil, fmt.Errorf("invalid GramJS session format")
	}
	b64 := raw[1:]
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(b64)
	}
	if err != nil || len(data) < 260 {
		return nil, fmt.Errorf("invalid binary session payload")
	}

	dcID := int(data[0])
	ipLen := int(data[2])
	if 3+ipLen+2+256 > len(data) {
		return nil, fmt.Errorf("session payload truncated")
	}
	ip := string(data[3 : 3+ipLen])
	port := int(data[3+ipLen])<<8 | int(data[3+ipLen+1])
	authKey := data[3+ipLen+2 : 3+ipLen+2+256]

	h := sha1.Sum(authKey)
	authKeyID := h[12:20]

	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	return &session.Data{
		DC:        dcID,
		Addr:      addr,
		AuthKey:   authKey,
		AuthKeyID: authKeyID,
	}, nil
}

func (ub *UserbotManager) Start(ctx context.Context) error {
	if ub.cfg.TelegramAPIID == 0 || ub.cfg.TelegramAPIHash == "" || ub.cfg.TelegramSessionString == "" {
		log.Println("[Userbot] Credentials not configured. MTProto Userbot disabled.")
		return nil
	}

	sessData, err := parseGramJSSession(ub.cfg.TelegramSessionString)
	if err != nil {
		log.Printf("⚠️ [Userbot] Failed to parse session string: %v", err)
		return nil
	}

	memStorage := &session.StorageMemory{}
	if err := memStorage.StoreSession(ctx, sessData); err != nil {
		log.Printf("⚠️ [Userbot] Failed to store session: %v", err)
		return nil
	}

	dispatcher := tg.NewUpdateDispatcher()

	handleMessage := func(ctx context.Context, e tg.Entities, msg *tg.Message) error {
		if msg == nil || msg.Out {
			return nil // ignore outgoing messages sent by self
		}

		senderID := int64(0)
		firstName := "User"
		username := ""

		if peerUser, ok := msg.FromID.(*tg.PeerUser); ok {
			senderID = peerUser.UserID
			if u, ok := e.Users[senderID]; ok {
				firstName = u.FirstName
				username = u.Username
			}
		} else if peerUser, ok := msg.PeerID.(*tg.PeerUser); ok {
			senderID = peerUser.UserID
			if u, ok := e.Users[senderID]; ok {
				firstName = u.FirstName
				username = u.Username
			}
		}

		chatID := senderID
		chatType := "private"
		title := ""

		if peerChat, ok := msg.PeerID.(*tg.PeerChat); ok {
			chatID = peerChat.ChatID
			chatType = "group"
			if c, ok := e.Chats[chatID]; ok {
				title = c.Title
			}
		} else if peerChannel, ok := msg.PeerID.(*tg.PeerChannel); ok {
			chatID = peerChannel.ChannelID
			chatType = "supergroup"
			if ch, ok := e.Channels[chatID]; ok {
				title = ch.Title
			}
		}

		log.Printf("[Userbot MTProto Message] chat=%d (%s), from=%s (@%s, ID: %d), text=%q",
			chatID, chatType, firstName, username, senderID, msg.Message)

		update := &domain.TelegramUpdate{
			UpdateID: msg.ID,
			Message: &domain.TelegramMessage{
				MessageID: msg.ID,
				From: &domain.TelegramUser{
					ID:        senderID,
					FirstName: firstName,
					Username:  username,
				},
				Chat: domain.TelegramChat{
					ID:        chatID,
					Type:      chatType,
					Title:     title,
					FirstName: firstName,
					Username:  username,
				},
				Text: msg.Message,
				Date: int64(msg.Date),
			},
		}

		if ub.onMessage != nil {
			ub.onMessage(ctx, update)
		}
		return nil
	}

	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		if msg, ok := u.Message.(*tg.Message); ok {
			return handleMessage(ctx, e, msg)
		}
		return nil
	})

	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		if msg, ok := u.Message.(*tg.Message); ok {
			return handleMessage(ctx, e, msg)
		}
		return nil
	})

	gaps := updates.New(updates.Config{
		Handler: dispatcher,
	})

	client := telegram.NewClient(ub.cfg.TelegramAPIID, ub.cfg.TelegramAPIHash, telegram.Options{
		SessionStorage: memStorage,
		UpdateHandler:  gaps,
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			log.Printf("[Userbot MTProto] Connecting to Telegram MTProto for @%s...", ub.MyUsername)
			err := client.Run(ctx, func(ctx context.Context) error {
				log.Printf("🎉 [Userbot MTProto] Connected and actively listening for messages sent to @%s (%s)!", ub.MyUsername, ub.MyName)
				<-ctx.Done()
				return ctx.Err()
			})

			if err != nil && ctx.Err() == nil {
				log.Printf("⚠️ [Userbot MTProto] Connection closed: %v. Reconnecting in 5s...", err)
				time.Sleep(5 * time.Second)
			}
		}
	}()

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
