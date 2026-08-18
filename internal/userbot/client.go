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
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/uploader"
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

	peerMu    sync.RWMutex
	peerCache map[int64]tg.InputPeerClass

	senderMu sync.RWMutex
	sender   *message.Sender
	uploader *uploader.Uploader
	rawAPI   *tg.Client
	peers    *peers.Manager
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
		peerCache:  make(map[int64]tg.InputPeerClass),
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
	loader := session.Loader{Storage: memStorage}
	if err := loader.Save(ctx, sessData); err != nil {
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
		var inputPeer tg.InputPeerClass

		if peerUser, ok := msg.FromID.(*tg.PeerUser); ok {
			senderID = peerUser.UserID
			if u, ok := e.Users[senderID]; ok {
				firstName = u.FirstName
				username = u.Username
				inputPeer = u.AsInputPeer()
			}
		} else if peerUser, ok := msg.PeerID.(*tg.PeerUser); ok {
			senderID = peerUser.UserID
			if u, ok := e.Users[senderID]; ok {
				firstName = u.FirstName
				username = u.Username
				inputPeer = u.AsInputPeer()
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
				inputPeer = c.AsInputPeer()
			}
		} else if peerChannel, ok := msg.PeerID.(*tg.PeerChannel); ok {
			chatID = peerChannel.ChannelID
			chatType = "supergroup"
			if ch, ok := e.Channels[chatID]; ok {
				title = ch.Title
				inputPeer = ch.AsInputPeer()
			}
		}

		if inputPeer != nil {
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			ub.peerMu.Unlock()
		}

		log.Printf("[Userbot MTProto Message] chat=%d (%s), from=%s (@%s, ID: %d), text=%q",
			chatID, chatType, firstName, username, senderID, msg.Message)

		update := &domain.TelegramUpdate{
			UpdateID: msg.ID,
			Message: &domain.TelegramMessage{
				MessageID: msg.ID,
				IsUserbot: true,
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
				rawAPI := tg.NewClient(client)
				snd := message.NewSender(rawAPI)
				upld := uploader.NewUploader(rawAPI)
				peersMgr := peers.Options{}.Build(rawAPI)
				_ = peersMgr.Init(ctx)

				ub.senderMu.Lock()
				ub.rawAPI = rawAPI
				ub.sender = snd
				ub.uploader = upld
				ub.peers = peersMgr
				ub.senderMu.Unlock()

				log.Printf("🎉 [Userbot MTProto] Connected and actively listening for messages sent to @%s (%s)!", ub.MyUsername, ub.MyName)
				<-ctx.Done()

				ub.senderMu.Lock()
				ub.rawAPI = nil
				ub.sender = nil
				ub.uploader = nil
				ub.peers = nil
				ub.senderMu.Unlock()

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

// IsAvailable checks if the MTProto userbot connection is live and ready to send messages.
func (ub *UserbotManager) IsAvailable() bool {
	ub.senderMu.RLock()
	defer ub.senderMu.RUnlock()
	return ub.sender != nil
}

// getInputPeer retrieves or dynamically resolves the InputPeer for a given chat ID.
func (ub *UserbotManager) getInputPeer(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	ub.peerMu.RLock()
	peer, exists := ub.peerCache[chatID]
	ub.peerMu.RUnlock()

	if exists && peer != nil {
		return peer, nil
	}

	ub.senderMu.RLock()
	peersMgr := ub.peers
	ub.senderMu.RUnlock()

	if peersMgr != nil {
		// 1. Try resolving as user
		if u, err := peersMgr.ResolveUserID(ctx, chatID); err == nil {
			inputPeer := u.InputPeer()
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			ub.peerMu.Unlock()
			return inputPeer, nil
		}
		// 2. Try resolving as chat
		if p, err := peersMgr.ResolvePeer(ctx, &tg.PeerChat{ChatID: chatID}); err == nil && p != nil {
			inputPeer := p.InputPeer()
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			ub.peerMu.Unlock()
			return inputPeer, nil
		}
		// 3. Try resolving as channel/supergroup
		if p, err := peersMgr.ResolvePeer(ctx, &tg.PeerChannel{ChannelID: chatID}); err == nil && p != nil {
			inputPeer := p.InputPeer()
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			ub.peerMu.Unlock()
			return inputPeer, nil
		}
	}

	return nil, fmt.Errorf("no cached or resolvable peer found for chat ID %d", chatID)
}

// SendMessage sends a text message from the real personal account via MTProto.
func (ub *UserbotManager) SendMessage(ctx context.Context, chatID int64, text string, replyToID int) error {
	ub.senderMu.RLock()
	snd := ub.sender
	ub.senderMu.RUnlock()

	if snd == nil {
		return fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, chatID)
	if err != nil {
		return err
	}

	var sendErr error
	if replyToID > 0 {
		_, sendErr = snd.To(peer).Reply(replyToID).Text(ctx, text)
	} else {
		_, sendErr = snd.To(peer).Text(ctx, text)
	}

	if sendErr != nil {
		return fmt.Errorf("MTProto SendMessage error: %w", sendErr)
	}
	return nil
}

// SendPhoto sends a photo from the real personal account via MTProto.
func (ub *UserbotManager) SendPhoto(ctx context.Context, chatID int64, photoData []byte, photoURL, caption string, replyToID int) error {
	ub.senderMu.RLock()
	snd := ub.sender
	upld := ub.uploader
	ub.senderMu.RUnlock()

	if snd == nil {
		return fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, chatID)
	if err != nil {
		return err
	}

	if len(photoData) > 0 && upld != nil {
		upload, err := upld.FromBytes(ctx, "photo.jpg", photoData)
		if err != nil {
			return fmt.Errorf("failed to upload photo to MTProto: %w", err)
		}
		var photoMedia message.MediaOption
		if caption != "" {
			photoMedia = message.UploadedPhoto(upload, styling.Plain(caption))
		} else {
			photoMedia = message.UploadedPhoto(upload)
		}

		if replyToID > 0 {
			_, err = snd.To(peer).Reply(replyToID).Media(ctx, photoMedia)
		} else {
			_, err = snd.To(peer).Media(ctx, photoMedia)
		}
		return err
	}

	if photoURL != "" {
		msg := photoURL
		if caption != "" {
			msg = caption + "\n" + photoURL
		}
		if replyToID > 0 {
			_, err := snd.To(peer).Reply(replyToID).Text(ctx, msg)
			return err
		}
		_, err := snd.To(peer).Text(ctx, msg)
		return err
	}

	return nil
}

// SendVoice sends an audio/voice note from the real personal account via MTProto.
func (ub *UserbotManager) SendVoice(ctx context.Context, chatID int64, voiceData []byte, replyToID int) error {
	ub.senderMu.RLock()
	snd := ub.sender
	upld := ub.uploader
	ub.senderMu.RUnlock()

	if snd == nil || upld == nil {
		return fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, chatID)
	if err != nil {
		return err
	}

	upload, err := upld.FromBytes(ctx, "voice.ogg", voiceData)
	if err != nil {
		return fmt.Errorf("failed to upload voice to MTProto: %w", err)
	}

	voiceMedia := message.UploadedDocument(upload).Voice()
	var sendErr error
	if replyToID > 0 {
		_, sendErr = snd.To(peer).Reply(replyToID).Media(ctx, voiceMedia)
	} else {
		_, sendErr = snd.To(peer).Media(ctx, voiceMedia)
	}
	return sendErr
}

// SetReaction sets a reaction emoji from the real personal account via MTProto.
func (ub *UserbotManager) SetReaction(ctx context.Context, chatID int64, msgID int, emoji string) error {
	ub.senderMu.RLock()
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if rawAPI == nil {
		return fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, chatID)
	if err != nil {
		return err
	}

	_, err = rawAPI.MessagesSendReaction(ctx, &tg.MessagesSendReactionRequest{
		Peer:  peer,
		MsgID: msgID,
		Reaction: []tg.ReactionClass{
			&tg.ReactionEmoji{Emoticon: emoji},
		},
	})
	return err
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

