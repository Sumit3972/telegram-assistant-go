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

func ParseGramJSSession(raw string) (*session.Data, error) {
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

func (ub *UserbotManager) extractReplyTo(ctx context.Context, e tg.Entities, msg *tg.Message, selfID int64) *domain.TelegramMessage {
	if msg.ReplyTo == nil {
		return nil
	}
	header, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
	if !ok || header.ReplyToMsgID <= 0 {
		return nil
	}

	replySenderID := int64(0)
	replyUsername := ""
	replyFirstName := ""
	replyText := ""
	var replyImageBase64 string

	if header.ReplyToPeerID != nil {
		if peerUser, ok := header.ReplyToPeerID.(*tg.PeerUser); ok {
			replySenderID = peerUser.UserID
			if u, ok := e.Users[replySenderID]; ok {
				replyUsername = u.Username
				replyFirstName = u.FirstName
			}
		}
	}

	if ub.rawAPI != nil {
		if peerChannel, ok := msg.PeerID.(*tg.PeerChannel); ok {
			if inputPeer, err := ub.getInputPeer(ctx, peerChannel.ChannelID); err == nil {
				if inCh, ok := inputPeer.(*tg.InputPeerChannel); ok {
					res, err := ub.rawAPI.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
						Channel: &tg.InputChannel{ChannelID: inCh.ChannelID, AccessHash: inCh.AccessHash},
						ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: header.ReplyToMsgID}},
					})
					if err == nil {
						switch v := res.(type) {
						case *tg.MessagesChannelMessages:
							for _, m := range v.Messages {
								if fullM, ok := m.(*tg.Message); ok {
									replyText = fullM.Message
									if fullM.Media != nil {
										if img, err := ub.downloadMediaBase64(ctx, fullM.Media); err == nil && img != "" {
											replyImageBase64 = img
										}
									}
									if replySenderID == 0 {
										if fullM.Out {
											replySenderID = selfID
											replyUsername = ub.MyUsername
											replyFirstName = ub.MyName
										} else if pUser, ok := fullM.FromID.(*tg.PeerUser); ok {
											replySenderID = pUser.UserID
											for _, u := range v.Users {
												if user, ok := u.(*tg.User); ok && user.ID == replySenderID {
													replyUsername = user.Username
													replyFirstName = user.FirstName
													break
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		} else {
			res, err := ub.rawAPI.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: header.ReplyToMsgID}})
			if err == nil {
				switch v := res.(type) {
				case *tg.MessagesMessages:
					for _, m := range v.Messages {
						if fullM, ok := m.(*tg.Message); ok {
							replyText = fullM.Message
							if fullM.Media != nil {
								if img, err := ub.downloadMediaBase64(ctx, fullM.Media); err == nil && img != "" {
									replyImageBase64 = img
								}
							}
							if replySenderID == 0 {
								if fullM.Out {
									replySenderID = selfID
									replyUsername = ub.MyUsername
									replyFirstName = ub.MyName
								} else if pUser, ok := fullM.FromID.(*tg.PeerUser); ok {
									replySenderID = pUser.UserID
									for _, u := range v.Users {
										if user, ok := u.(*tg.User); ok && user.ID == replySenderID {
											replyUsername = user.Username
											replyFirstName = user.FirstName
											break
										}
									}
								}
							}
						}
					}
				case *tg.MessagesMessagesSlice:
					for _, m := range v.Messages {
						if fullM, ok := m.(*tg.Message); ok {
							replyText = fullM.Message
							if fullM.Media != nil {
								if img, err := ub.downloadMediaBase64(ctx, fullM.Media); err == nil && img != "" {
									replyImageBase64 = img
								}
							}
							if replySenderID == 0 {
								if fullM.Out {
									replySenderID = selfID
									replyUsername = ub.MyUsername
									replyFirstName = ub.MyName
								} else if pUser, ok := fullM.FromID.(*tg.PeerUser); ok {
									replySenderID = pUser.UserID
									for _, u := range v.Users {
										if user, ok := u.(*tg.User); ok && user.ID == replySenderID {
											replyUsername = user.Username
											replyFirstName = user.FirstName
											break
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return &domain.TelegramMessage{
		MessageID: header.ReplyToMsgID,
		From: &domain.TelegramUser{
			ID:        replySenderID,
			Username:  replyUsername,
			FirstName: replyFirstName,
		},
		Text:        replyText,
		ImageBase64: replyImageBase64,
	}
}

func (ub *UserbotManager) Start(ctx context.Context) error {
	if ub.cfg.TelegramAPIID == 0 || ub.cfg.TelegramAPIHash == "" || ub.cfg.TelegramSessionString == "" {
		log.Println("[Userbot] Credentials not configured. MTProto Userbot disabled.")
		return nil
	}

	sessData, err := ParseGramJSSession(ub.cfg.TelegramSessionString)
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
		if msg == nil {
			return nil
		}

		if msg.Out {
			chatID := int64(0)
			chatType := "private"
			title := ""
			var inputPeer tg.InputPeerClass

			if peerUser, ok := msg.PeerID.(*tg.PeerUser); ok {
				chatID = peerUser.UserID
				chatType = "private"
				if u, ok := e.Users[chatID]; ok {
					inputPeer = u.AsInputPeer()
					title = strings.TrimSpace(u.FirstName + " " + u.LastName)
				}
				if inputPeer == nil && chatID > 0 {
					ub.peerMu.RLock()
					inputPeer = ub.peerCache[chatID]
					ub.peerMu.RUnlock()
				}
			} else if peerChat, ok := msg.PeerID.(*tg.PeerChat); ok {
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
				if chatType == "supergroup" {
					botChatID := -1000000000000 - chatID
					ub.peerCache[botChatID] = inputPeer
				} else if chatType == "private" && chatID > 0 {
					ub.peerCache[chatID] = inputPeer
				}
				ub.peerMu.Unlock()
			}

			selfID := int64(8542441463)
			if ub.MyUserID != "" {
				if id, err := strconv.ParseInt(ub.MyUserID, 10, 64); err == nil && id > 0 {
					selfID = id
				}
			}

			selfUsername := ub.MyUsername
			if selfUsername == "" {
				selfUsername = "Sumit3972"
			}
			selfName := ub.MyName
			if selfName == "" {
				selfName = "Sumit"
			}

			var replyToMsg *domain.TelegramMessage
			if msg.ReplyTo != nil {
				replyToMsg = ub.extractReplyTo(ctx, e, msg, selfID)
			}

			if chatID != 0 && ub.onMessage != nil {
				ub.onMessage(ctx, &domain.TelegramUpdate{
					UpdateID: msg.ID,
					Message: &domain.TelegramMessage{
						MessageID: msg.ID,
						IsUserbot: true,
						From: &domain.TelegramUser{
							ID:        selfID,
							FirstName: selfName,
							Username:  selfUsername,
						},
						Chat: domain.TelegramChat{
							ID:        chatID,
							Type:      chatType,
							Title:     title,
							FirstName: selfName,
							Username:  selfUsername,
						},
						ReplyToMessage: replyToMsg,
						Text:           msg.Message,
						Date:           int64(msg.Date),
					},
				})
			}
			return nil
		}

		senderID := int64(0)
		firstName := ""
		username := ""
		var inputPeer tg.InputPeerClass

		if peerUser, ok := msg.FromID.(*tg.PeerUser); ok {
			senderID = peerUser.UserID
			if u, ok := e.Users[senderID]; ok {
				firstName = strings.TrimSpace(u.FirstName)
				if firstName == "" && u.LastName != "" {
					firstName = strings.TrimSpace(u.LastName)
				}
				username = strings.TrimSpace(u.Username)
				inputPeer = u.AsInputPeer()
			}
		} else if peerUser, ok := msg.PeerID.(*tg.PeerUser); ok {
			senderID = peerUser.UserID
			if u, ok := e.Users[senderID]; ok {
				firstName = strings.TrimSpace(u.FirstName)
				if firstName == "" && u.LastName != "" {
					firstName = strings.TrimSpace(u.LastName)
				}
				username = strings.TrimSpace(u.Username)
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
			} else {
				ub.peerMu.RLock()
				inputPeer = ub.peerCache[chatID]
				ub.peerMu.RUnlock()
			}
		}

		if inputPeer == nil && chatType == "private" && senderID > 0 {
			ub.peerMu.RLock()
			inputPeer = ub.peerCache[senderID]
			ub.peerMu.RUnlock()
		}

		if inputPeer != nil {
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			if chatType == "supergroup" {
				// Also cache with Telegram Bot API -100 prefix format
				botChatID := -1000000000000 - chatID
				ub.peerCache[botChatID] = inputPeer
			} else if chatType == "private" && senderID > 0 {
				ub.peerCache[senderID] = inputPeer
			}
			ub.peerMu.Unlock()
		}

		log.Printf("[Userbot MTProto Message] chat=%d (%s), from=%s (@%s, ID: %d), text=%q",
			chatID, chatType, firstName, username, senderID, msg.Message)

		selfID := int64(8542441463)
		if ub.MyUserID != "" {
			if id, err := strconv.ParseInt(ub.MyUserID, 10, 64); err == nil && id > 0 {
				selfID = id
			}
		}

		replyToMsg := ub.extractReplyTo(ctx, e, msg, selfID)

		var imageBase64 string
		if msg.Media != nil && ub.rawAPI != nil {
			if img, err := ub.downloadMediaBase64(ctx, msg.Media); err == nil && img != "" {
				imageBase64 = img
			}
		}

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
				ReplyToMessage: replyToMsg,
				Text:           msg.Message,
				ImageBase64:    imageBase64,
				Date:           int64(msg.Date),
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

				if self, err := client.Self(ctx); err == nil && self != nil {
					ub.MyUserID = fmt.Sprintf("%d", self.ID)
					if self.Username != "" {
						ub.MyUsername = self.Username
					}
					if self.FirstName != "" {
						ub.MyName = self.FirstName
					}
					ub.cfg.MyPersonalUserID = ub.MyUserID
					ub.cfg.MyPersonalUsername = ub.MyUsername
					log.Printf("🎉 [Userbot MTProto] Authenticated as real account: @%s (Name: %q, ID: %d)!", self.Username, self.FirstName, self.ID)
				} else {
					log.Printf("🎉 [Userbot MTProto] Connected and actively listening for messages sent to @%s (%s)!", ub.MyUsername, ub.MyName)
				}

				// Pre-cache authentic AccessHash for all channels, supergroups, and users on startup
				go func() {
					time.Sleep(500 * time.Millisecond)
					_ = ub.syncDialogs(ctx)
				}()

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

// syncDialogs fetches all dialogs (supergroups, groups, users) and pre-populates the peer cache with genuine AccessHashes.
func (ub *UserbotManager) syncDialogs(ctx context.Context) error {
	ub.senderMu.RLock()
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if rawAPI == nil {
		return fmt.Errorf("userbot MTProto client is not ready")
	}

	dialogsRes, err := rawAPI.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      100,
	})
	if err != nil {
		log.Printf("⚠️ [Userbot MTProto] Failed to sync dialogs: %v", err)
		return err
	}

	var allChats []tg.ChatClass
	var allUsers []tg.UserClass

	switch d := dialogsRes.(type) {
	case *tg.MessagesDialogs:
		allChats = d.Chats
		allUsers = d.Users
	case *tg.MessagesDialogsSlice:
		allChats = d.Chats
		allUsers = d.Users
	}

	channelCount := 0
	chatCount := 0
	userCount := 0

	ub.peerMu.Lock()
	for _, c := range allChats {
		switch ch := c.(type) {
		case *tg.Channel:
			p := &tg.InputPeerChannel{
				ChannelID:  ch.ID,
				AccessHash: ch.AccessHash,
			}
			ub.peerCache[ch.ID] = p
			ub.peerCache[-1000000000000-ch.ID] = p
			channelCount++
		case *tg.Chat:
			p := &tg.InputPeerChat{
				ChatID: ch.ID,
			}
			ub.peerCache[ch.ID] = p
			ub.peerCache[-ch.ID] = p
			chatCount++
		}
	}

	for _, u := range allUsers {
		if user, ok := u.(*tg.User); ok {
			p := &tg.InputPeerUser{
				UserID:     user.ID,
				AccessHash: user.AccessHash,
			}
			ub.peerCache[user.ID] = p
			userCount++
		}
	}
	ub.peerMu.Unlock()

	log.Printf("🎉 [Userbot MTProto] Synced dialogs into peer cache: %d channels/supergroups, %d group chats, %d users",
		channelCount, chatCount, userCount)
	return nil
}

// getInputPeer retrieves or dynamically resolves the InputPeer for a given chat ID.
func (ub *UserbotManager) getInputPeer(ctx context.Context, chatID int64) (tg.InputPeerClass, error) {
	if ub.MyUserID != "" {
		if selfID, err := strconv.ParseInt(ub.MyUserID, 10, 64); err == nil && chatID == selfID {
			return &tg.InputPeerSelf{}, nil
		}
	}

	ub.peerMu.RLock()
	peer, exists := ub.peerCache[chatID]
	ub.peerMu.RUnlock()

	if exists && peer != nil {
		return peer, nil
	}

	// Normalize channel ID if negative (Bot API format -100xxx or normal chat -xxx)
	channelID := chatID
	if channelID < -1000000000000 {
		channelID = -1 * (channelID + 1000000000000)
	} else if channelID < 0 {
		channelID = -channelID
	}

	// Check with normalized channelID as well
	ub.peerMu.RLock()
	peer, exists = ub.peerCache[channelID]
	if !exists || peer == nil {
		botChatID := -1000000000000 - channelID
		peer, exists = ub.peerCache[botChatID]
	}
	ub.peerMu.RUnlock()
	if exists && peer != nil {
		return peer, nil
	}

	// Dynamically sync dialogs if missing
	log.Printf("[Userbot MTProto] Peer for chat %d not found in cache. Refreshing dialogs...", chatID)
	_ = ub.syncDialogs(ctx)

	ub.peerMu.RLock()
	peer, exists = ub.peerCache[chatID]
	if !exists || peer == nil {
		peer, exists = ub.peerCache[channelID]
	}
	if !exists || peer == nil {
		botChatID := -1000000000000 - channelID
		peer, exists = ub.peerCache[botChatID]
	}
	ub.peerMu.RUnlock()

	if exists && peer != nil {
		return peer, nil
	}

	ub.senderMu.RLock()
	peersMgr := ub.peers
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if peersMgr != nil {
		if p, err := peersMgr.ResolvePeer(ctx, &tg.PeerChannel{ChannelID: channelID}); err == nil && p != nil {
			inputPeer := p.InputPeer()
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			ub.peerCache[channelID] = inputPeer
			ub.peerMu.Unlock()
			return inputPeer, nil
		}
		if p, err := peersMgr.ResolvePeer(ctx, &tg.PeerChat{ChatID: channelID}); err == nil && p != nil {
			inputPeer := p.InputPeer()
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			ub.peerCache[channelID] = inputPeer
			ub.peerMu.Unlock()
			return inputPeer, nil
		}
		if u, err := peersMgr.ResolveUserID(ctx, chatID); err == nil {
			inputPeer := u.InputPeer()
			ub.peerMu.Lock()
			ub.peerCache[chatID] = inputPeer
			ub.peerMu.Unlock()
			return inputPeer, nil
		}
	}

	if rawAPI != nil {
		res, err := rawAPI.UsersGetUsers(ctx, []tg.InputUserClass{
			&tg.InputUser{UserID: chatID, AccessHash: 0},
		})
		if err == nil && len(res) > 0 {
			if u, ok := res[0].AsNotEmpty(); ok {
				accessHash, _ := u.GetAccessHash()
				inputPeer := &tg.InputPeerUser{
					UserID:     u.GetID(),
					AccessHash: accessHash,
				}
				ub.peerMu.Lock()
				ub.peerCache[chatID] = inputPeer
				ub.peerMu.Unlock()
				return inputPeer, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to resolve InputPeer for chat %d (missing AccessHash)", chatID)
}

func extractMsgIDFromUpdates(updates tg.UpdatesClass) int {
	switch u := updates.(type) {
	case *tg.UpdateShortSentMessage:
		return u.ID
	case *tg.Updates:
		for _, up := range u.Updates {
			switch m := up.(type) {
			case *tg.UpdateNewMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			case *tg.UpdateNewChannelMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, up := range u.Updates {
			switch m := up.(type) {
			case *tg.UpdateNewMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			case *tg.UpdateNewChannelMessage:
				if msg, ok := m.Message.(*tg.Message); ok {
					return msg.ID
				}
			}
		}
	}
	return 0
}

// SendMessageGetID sends a text message and returns the sent message's ID.
func (ub *UserbotManager) SendMessageGetID(ctx context.Context, chatID int64, text string, replyToID int) (int, error) {
	ub.senderMu.RLock()
	snd := ub.sender
	ub.senderMu.RUnlock()

	if snd == nil {
		return 0, fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, chatID)
	if err != nil {
		return 0, fmt.Errorf("userbot getInputPeer failed for %d: %w", chatID, err)
	}

	var updates tg.UpdatesClass
	var sendErr error
	if replyToID > 0 {
		updates, sendErr = snd.To(peer).Reply(replyToID).Text(ctx, text)
	} else {
		updates, sendErr = snd.To(peer).Text(ctx, text)
	}

	// If replying failed with MSG_ID_INVALID or any message ID error, retry direct text without reply
	if sendErr != nil && replyToID > 0 {
		log.Printf("[Userbot MTProto] Reply to msg %d failed in %d (%v). Retrying direct text without reply...", replyToID, chatID, sendErr)
		updates, sendErr = snd.To(peer).Text(ctx, text)
	}

	// If CHANNEL_INVALID or PEER_ID_INVALID, refresh dialogs and retry send
	if sendErr != nil && (strings.Contains(sendErr.Error(), "CHANNEL_INVALID") || strings.Contains(sendErr.Error(), "PEER_ID_INVALID")) {
		log.Printf("[Userbot MTProto] Got %v for chat %d. Refreshing dialogs and retrying send...", sendErr, chatID)
		_ = ub.syncDialogs(ctx)
		if refreshedPeer, pErr := ub.getInputPeer(ctx, chatID); pErr == nil {
			updates, sendErr = snd.To(refreshedPeer).Text(ctx, text)
		}
	}

	if sendErr != nil {
		log.Printf("❌ [Userbot MTProto] SendMessage to %d failed: %v", chatID, sendErr)
		return 0, sendErr
	}

	sentID := extractMsgIDFromUpdates(updates)
	log.Printf("✅ [Userbot MTProto] SendMessage succeeded in chat %d! (msgID: %d)", chatID, sentID)
	return sentID, nil
}

// SendMessage sends a text message from the real personal account via MTProto.
func (ub *UserbotManager) SendMessage(ctx context.Context, chatID int64, text string, replyToID int) error {
	_, err := ub.SendMessageGetID(ctx, chatID, text, replyToID)
	return err
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
		return fmt.Errorf("userbot getInputPeer failed for %d: %w", chatID, err)
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

		var sendErr error
		if replyToID > 0 {
			_, sendErr = snd.To(peer).Reply(replyToID).Media(ctx, photoMedia)
		} else {
			_, sendErr = snd.To(peer).Media(ctx, photoMedia)
		}

		if sendErr != nil && replyToID > 0 {
			log.Printf("[Userbot MTProto] Reply to msg %d failed while sending photo (%v). Retrying directly...", replyToID, sendErr)
			_, sendErr = snd.To(peer).Media(ctx, photoMedia)
		}

		if sendErr != nil && (strings.Contains(sendErr.Error(), "CHANNEL_INVALID") || strings.Contains(sendErr.Error(), "PEER_ID_INVALID")) {
			log.Printf("[Userbot MTProto] Got %v while sending photo to %d. Refreshing dialogs...", sendErr, chatID)
			_ = ub.syncDialogs(ctx)
			if refreshedPeer, pErr := ub.getInputPeer(ctx, chatID); pErr == nil {
				_, sendErr = snd.To(refreshedPeer).Media(ctx, photoMedia)
			}
		}

		if sendErr != nil {
			log.Printf("❌ [Userbot MTProto] SendPhoto failed: %v", sendErr)
			return sendErr
		}
		log.Printf("✅ [Userbot MTProto] SendPhoto succeeded in chat %d!", chatID)
		return nil
	}

	if photoURL != "" {
		msg := photoURL
		if caption != "" {
			msg = caption + "\n" + photoURL
		}
		var sendErr error
		if replyToID > 0 {
			_, sendErr = snd.To(peer).Reply(replyToID).Text(ctx, msg)
		} else {
			_, sendErr = snd.To(peer).Text(ctx, msg)
		}
		if sendErr != nil && replyToID > 0 {
			_, sendErr = snd.To(peer).Text(ctx, msg)
		}
		return sendErr
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
		return fmt.Errorf("userbot getInputPeer failed for %d: %w", chatID, err)
	}

	upload, err := upld.FromBytes(ctx, "voice.ogg", voiceData)
	if err != nil {
		return fmt.Errorf("failed to upload voice to MTProto: %w", err)
	}

	voiceMedia := message.UploadedDocument(upload).MIME("audio/ogg").Voice()
	var sendErr error
	if replyToID > 0 {
		_, sendErr = snd.To(peer).Reply(replyToID).Media(ctx, voiceMedia)
	} else {
		_, sendErr = snd.To(peer).Media(ctx, voiceMedia)
	}

	if sendErr != nil && replyToID > 0 {
		log.Printf("[Userbot MTProto] Reply to msg %d failed while sending voice (%v). Retrying directly...", replyToID, sendErr)
		_, sendErr = snd.To(peer).Media(ctx, voiceMedia)
	}

	if sendErr != nil && (strings.Contains(sendErr.Error(), "CHANNEL_INVALID") || strings.Contains(sendErr.Error(), "PEER_ID_INVALID")) {
		log.Printf("[Userbot MTProto] Got %v while sending voice to %d. Refreshing dialogs...", sendErr, chatID)
		_ = ub.syncDialogs(ctx)
		if refreshedPeer, pErr := ub.getInputPeer(ctx, chatID); pErr == nil {
			_, sendErr = snd.To(refreshedPeer).Media(ctx, voiceMedia)
		}
	}

	if sendErr != nil {
		log.Printf("❌ [Userbot MTProto] SendVoice failed: %v", sendErr)
		return sendErr
	}
	log.Printf("✅ [Userbot MTProto] SendVoice succeeded in chat %d!", chatID)
	return nil
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

// SendChatAction sends real-time typing / recording / uploading action from the real personal account.
func (ub *UserbotManager) SendChatAction(ctx context.Context, chatID int64, action string) error {
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

	var tgAction tg.SendMessageActionClass
	switch action {
	case "upload_photo":
		tgAction = &tg.SendMessageUploadPhotoAction{}
	case "record_voice":
		tgAction = &tg.SendMessageRecordAudioAction{}
	default:
		tgAction = &tg.SendMessageTypingAction{}
	}

	_, err = rawAPI.MessagesSetTyping(ctx, &tg.MessagesSetTypingRequest{
		Peer:   peer,
		Action: tgAction,
	})
	return err
}

// MarkAsRead sends read receipt (double blue ticks) natively via MTProto.
func (ub *UserbotManager) MarkAsRead(ctx context.Context, chatID int64, maxMsgID int) error {
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

	switch p := peer.(type) {
	case *tg.InputPeerChannel:
		_, err = rawAPI.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{
			Channel: &tg.InputChannel{
				ChannelID:  p.ChannelID,
				AccessHash: p.AccessHash,
			},
			MaxID: maxMsgID,
		})
	default:
		_, err = rawAPI.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{
			Peer:  peer,
			MaxID: maxMsgID,
		})
	}
	if err == nil {
		log.Printf("[Userbot] ✅ Marked history as read (double tick) in chat %d up to msg %d", chatID, maxMsgID)
	}
	return err
}

// EditMessage edits a previously sent message via MTProto MessagesEditMessage.
func (ub *UserbotManager) EditMessage(ctx context.Context, chatID int64, msgID int, newText string) error {
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

	_, err = rawAPI.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      msgID,
		Message: newText,
	})
	if err != nil {
		log.Printf("[Userbot] ⚠️ EditMessage failed in chat %d for msg %d: %v", chatID, msgID, err)
		return err
	}
	log.Printf("[Userbot] ✏️ Successfully edited msg %d in chat %d to: %q", msgID, chatID, newText)
	return nil
}

// ViewPeerStories marks stories from a user/peer as viewed.
func (ub *UserbotManager) ViewPeerStories(ctx context.Context, peerID int64) error {
	ub.senderMu.RLock()
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if rawAPI == nil {
		return fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, peerID)
	if err != nil {
		return err
	}

	_, err = rawAPI.StoriesReadStories(ctx, &tg.StoriesReadStoriesRequest{
		Peer: peer,
	})
	if err == nil {
		log.Printf("[Userbot] 👁️ Viewed stories for peer %d", peerID)
	}
	return err
}


// BlockUser blocks a user natively on Telegram via MTProto ContactsBlock.
func (ub *UserbotManager) BlockUser(ctx context.Context, userID int64) error {
	ub.senderMu.RLock()
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if rawAPI == nil {
		return fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, userID)
	if err != nil {
		peer = &tg.InputPeerUser{UserID: userID}
	}

	_, err = rawAPI.ContactsBlock(ctx, &tg.ContactsBlockRequest{
		ID: peer,
	})
	if err != nil {
		log.Printf("[Userbot] ❌ Failed to block user %d via MTProto ContactsBlock: %v", userID, err)
		return err
	}

	log.Printf("[Userbot] 🚫 Successfully blocked user %d natively via MTProto ContactsBlock!", userID)
	return nil
}

// UnblockUser unblocks a user natively on Telegram via MTProto ContactsUnblock.
func (ub *UserbotManager) UnblockUser(ctx context.Context, userID int64) error {
	ub.senderMu.RLock()
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if rawAPI == nil {
		return fmt.Errorf("userbot MTProto client is not connected")
	}

	peer, err := ub.getInputPeer(ctx, userID)
	if err != nil {
		peer = &tg.InputPeerUser{UserID: userID}
	}

	_, err = rawAPI.ContactsUnblock(ctx, &tg.ContactsUnblockRequest{
		ID: peer,
	})
	if err != nil {
		log.Printf("[Userbot] ❌ Failed to unblock user %d via MTProto ContactsUnblock: %v", userID, err)
		return err
	}

	log.Printf("[Userbot] ✅ Successfully unblocked user %d natively via MTProto ContactsUnblock!", userID)
	return nil
}

// ResolveUsername resolves a Telegram username via MTProto ContactsResolveUsername.
func (ub *UserbotManager) ResolveUsername(ctx context.Context, username string) (int64, string, error) {
	ub.senderMu.RLock()
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if rawAPI == nil {
		return 0, "", fmt.Errorf("userbot MTProto client is not connected")
	}

	clean := strings.TrimPrefix(strings.TrimSpace(username), "@")
	if clean == "" {
		return 0, "", fmt.Errorf("username is empty")
	}

	res, err := rawAPI.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: clean,
	})
	if err != nil {
		return 0, "", err
	}

	for _, uClass := range res.GetUsers() {
		if u, ok := uClass.(*tg.User); ok {
			ub.peerMu.Lock()
			ub.peerCache[u.ID] = u.AsInputPeer()
			ub.peerMu.Unlock()
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			return u.ID, name, nil
		}
	}

	return 0, "", fmt.Errorf("no user found for username %s", clean)
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
	if strings.Contains(lower, nameLower) ||
		strings.Contains(lower, "chavi") || strings.Contains(lower, "chhavi") || strings.Contains(lower, "chabi") || strings.Contains(lower, "chaavi") || strings.Contains(lower, "chvai") {
		return true
	}

	return false
}

// downloadMediaBase64 downloads photo or document media from an MTProto message as a base64 data URI.
func (ub *UserbotManager) downloadMediaBase64(ctx context.Context, media tg.MessageMediaClass) (string, error) {
	if media == nil {
		return "", fmt.Errorf("media is nil")
	}

	ub.senderMu.RLock()
	rawAPI := ub.rawAPI
	ub.senderMu.RUnlock()

	if rawAPI == nil {
		return "", fmt.Errorf("rawAPI is nil")
	}

	var loc tg.InputFileLocationClass
	mime := "image/jpeg"

	switch v := media.(type) {
	case *tg.MessageMediaPhoto:
		if p, ok := v.Photo.(*tg.Photo); ok {
			loc = &tg.InputPhotoFileLocation{
				ID:            p.ID,
				AccessHash:    p.AccessHash,
				FileReference: p.FileReference,
				ThumbSize:     "m",
			}
		}
	case *tg.MessageMediaDocument:
		if doc, ok := v.Document.(*tg.Document); ok {
			loc = &tg.InputDocumentFileLocation{
				ID:            doc.ID,
				AccessHash:    doc.AccessHash,
				FileReference: doc.FileReference,
				ThumbSize:     "m",
			}
			if strings.HasPrefix(doc.MimeType, "image/") {
				mime = doc.MimeType
			}
		}
	}

	if loc == nil {
		return "", fmt.Errorf("unsupported media type for vision")
	}

	// 10-second timeout context for quick media download
	dlCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := rawAPI.UploadGetFile(dlCtx, &tg.UploadGetFileRequest{
		Location: loc,
		Offset:   0,
		Limit:    1024 * 1024, // 1MB limit for vision input
	})
	if err != nil {
		return "", err
	}

	if file, ok := res.(*tg.UploadFile); ok && len(file.Bytes) > 0 {
		return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(file.Bytes)), nil
	}

	return "", fmt.Errorf("empty file data returned")
}

