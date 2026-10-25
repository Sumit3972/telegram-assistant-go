package moderator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"telegram-ai-assistant/internal/ai"
	"telegram-ai-assistant/internal/decision"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/media"
	"telegram-ai-assistant/internal/prompt"
	"telegram-ai-assistant/internal/telegram"
)

type messageBatch struct {
	messages  []*domain.TelegramMessage
	texts     []string
	isAdmin   bool
	firstTime time.Time
	timer     *time.Timer
}

const (
	defaultDebounceDuration = 5500 * time.Millisecond // wait 5.5s for user to finish typing multiple rapid messages (matches Instagram debounce)
	defaultMaxBatchWait     = 12000 * time.Millisecond // maximum ceiling delay before flushing
)

type ConversationHandler struct {
	aiClient         *ai.Client
	decisionClient   *decision.Client
	imageService     *media.ImageService
	voiceService     *media.VoiceService
	searchService    *media.SearchService
	musicService     *media.MusicService
	historyRepo      domain.HistoryRepository
	relRepo          domain.RelationshipRepository
	groupRepo        domain.GroupRepository
	adminRepo        domain.AdminRepository
	warningRepo      domain.WarningRepository
	modLogRepo       domain.ModerationLogRepository
	blockedRepo      domain.BlockedUserRepository
	abuseRepo        domain.AbuseRecordRepository
	botClient        *telegram.BotClient
	userbotSender    domain.UserbotSender
	botName          string
	botUsername      string
	fallbackKey      string
	debounceDuration time.Duration
	maxBatchWait     time.Duration

	batchMu sync.Mutex
	batches map[string]*messageBatch

	chatLocks        sync.Map // chatID (string) -> *sync.Mutex
	groupCooldowns   sync.Map // chatID (string) -> time.Time
	inFlightPhotos   sync.Map // chatID (string) -> bool
	recentReplies    sync.Map // chatID (string) -> []string
	recentRepliesMu  sync.Mutex
	userPhotoPending sync.Map // userID (string) -> bool (true if Chavi sent photo, waiting for user photo before sending again)
}

func (h *ConversationHandler) hasUserPhotoPending(userID string) bool {
	if val, ok := h.userPhotoPending.Load(userID); ok {
		return val.(bool)
	}
	return false
}

func (h *ConversationHandler) markPhotoSentToUser(userID string) {
	h.userPhotoPending.Store(userID, true)
}

func (h *ConversationHandler) clearUserPhotoPending(userID string) {
	h.userPhotoPending.Store(userID, false)
}

func (h *ConversationHandler) getBotAPIChatID(msg *domain.TelegramMessage) int64 {
	botChatID := msg.Chat.ID
	if (msg.Chat.Type == "supergroup" || msg.Chat.Type == "channel") && botChatID > 0 {
		return -1000000000000 - botChatID
	}
	return botChatID
}

func (h *ConversationHandler) getChatLock(chatID int64) *sync.Mutex {
	key := strconv.FormatInt(chatID, 10)
	val, _ := h.chatLocks.LoadOrStore(key, &sync.Mutex{})
	return val.(*sync.Mutex)
}

func (h *ConversationHandler) getRecentReplies(chatID int64) []string {
	key := strconv.FormatInt(chatID, 10)
	if val, ok := h.recentReplies.Load(key); ok {
		if list, ok := val.([]string); ok {
			return list
		}
	}
	return nil
}

func (h *ConversationHandler) addRecentReply(chatID int64, reply string) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return
	}
	h.recentRepliesMu.Lock()
	defer h.recentRepliesMu.Unlock()
	key := strconv.FormatInt(chatID, 10)
	list := h.getRecentReplies(chatID)
	list = append(list, trimmed)
	if len(list) > 10 {
		list = list[len(list)-10:]
	}
	h.recentReplies.Store(key, list)
}

func (h *ConversationHandler) SetUserbotSender(s domain.UserbotSender) {
	h.userbotSender = s
}

func NewConversationHandler(
	aiClient *ai.Client,
	imageService *media.ImageService,
	voiceService *media.VoiceService,
	searchService *media.SearchService,
	musicService *media.MusicService,
	historyRepo domain.HistoryRepository,
	relRepo domain.RelationshipRepository,
	groupRepo domain.GroupRepository,
	adminRepo domain.AdminRepository,
	warningRepo domain.WarningRepository,
	modLogRepo domain.ModerationLogRepository,
	blockedRepo domain.BlockedUserRepository,
	abuseRepo domain.AbuseRecordRepository,
	botClient *telegram.BotClient,
	botName, botUsername, fallbackKey, codivKey string,
	debounceDuration time.Duration,
) *ConversationHandler {
	if debounceDuration <= 0 {
		debounceDuration = defaultDebounceDuration
	}
	maxBatchWait := debounceDuration * 2
	if maxBatchWait < defaultMaxBatchWait {
		maxBatchWait = defaultMaxBatchWait
	}

	return &ConversationHandler{
		aiClient:         aiClient,
		decisionClient:   decision.NewClient(codivKey),
		imageService:     imageService,
		voiceService:     voiceService,
		searchService:    searchService,
		musicService:     musicService,
		historyRepo:      historyRepo,
		relRepo:          relRepo,
		groupRepo:        groupRepo,
		adminRepo:        adminRepo,
		warningRepo:      warningRepo,
		modLogRepo:       modLogRepo,
		blockedRepo:      blockedRepo,
		abuseRepo:        abuseRepo,
		botClient:        botClient,
		botName:          botName,
		botUsername:      botUsername,
		fallbackKey:      fallbackKey,
		debounceDuration: debounceDuration,
		maxBatchWait:     maxBatchWait,
		batches:          make(map[string]*messageBatch),
	}
}

// EnqueueConversation buffers rapid sequential messages from the same user into a unified context
// so the AI replies once to the entire combined thought instead of sending fragmented, repetitive replies.
// Note: We do NOT trigger typing action while buffering; typing action only begins when debounce finishes and processing starts.
func (h *ConversationHandler) EnqueueConversation(ctx context.Context, msg *domain.TelegramMessage, isAdmin bool) {
	if msg == nil || msg.From == nil {
		return
	}
	if IsOwnerOrMom(msg) {
		isAdmin = true
	}
	// Drop Telegram official service messages (ID 777000 or username Telegram)
	if msg.Chat.ID == 777000 || msg.From.ID == 777000 || strings.EqualFold(msg.From.Username, "Telegram") {
		return
	}

	key := fmt.Sprintf("%d:%d", msg.Chat.ID, msg.From.ID)
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}

	h.batchMu.Lock()
	defer h.batchMu.Unlock()

	batch, exists := h.batches[key]
	if exists {
		if batch.timer != nil {
			batch.timer.Stop()
		}

		batch.messages = append(batch.messages, msg)
		if text != "" {
			batch.texts = append(batch.texts, text)
		}
		if isAdmin {
			batch.isAdmin = true
		}

		// Check if batch ceiling has been reached
		if time.Since(batch.firstTime) >= h.maxBatchWait {
			log.Printf("[Conversation] ⏰ Batch ceiling reached for %s. Flushing %d messages immediately...", key, len(batch.messages))
			delete(h.batches, key)
			go h.processBatch(batch)
			return
		}

		// Reset debounce timer to debounceDuration (e.g. 5.5s)
		batch.timer = time.AfterFunc(h.debounceDuration, func() {
			h.flushBatch(key)
		})
		log.Printf("[Conversation] ⏳ Buffered message %d from chat %d (waiting %v for more messages)...",
			len(batch.messages), msg.Chat.ID, h.debounceDuration)
		return
	}

	// New batch
	var texts []string
	if text != "" {
		texts = append(texts, text)
	}

	newBatch := &messageBatch{
		messages:  []*domain.TelegramMessage{msg},
		texts:     texts,
		isAdmin:   isAdmin,
		firstTime: time.Now(),
	}

	newBatch.timer = time.AfterFunc(h.debounceDuration, func() {
		h.flushBatch(key)
	})

	h.batches[key] = newBatch
	log.Printf("[Conversation] ⏳ Started debounce buffer in chat %d (waiting %v for burst messages)...",
		msg.Chat.ID, h.debounceDuration)
}

func (h *ConversationHandler) HasActiveBatch(chatID, userID int64) bool {
	h.batchMu.Lock()
	defer h.batchMu.Unlock()
	key := fmt.Sprintf("%d:%d", chatID, userID)
	_, exists := h.batches[key]
	return exists
}

func (h *ConversationHandler) flushBatch(key string) {
	h.batchMu.Lock()
	batch, exists := h.batches[key]
	if !exists {
		h.batchMu.Unlock()
		return
	}
	delete(h.batches, key)
	h.batchMu.Unlock()

	h.processBatch(batch)
}

func (h *ConversationHandler) processBatch(batch *messageBatch) {
	if batch == nil || len(batch.messages) == 0 {
		return
	}

	if len(batch.messages) == 1 {
		h.HandleConversation(context.Background(), batch.messages[0], batch.isAdmin)
		return
	}

	lastMsg := batch.messages[len(batch.messages)-1]
	mergedMsg := *lastMsg

	var cleanTexts []string
	for _, t := range batch.texts {
		trimmed := strings.TrimSpace(t)
		if trimmed != "" {
			cleanTexts = append(cleanTexts, trimmed)
		}
	}
	if len(cleanTexts) > 0 {
		mergedMsg.Text = strings.Join(cleanTexts, "\n")
	}

	for _, m := range batch.messages {
		if mergedMsg.ImageBase64 == "" && m.ImageBase64 != "" {
			mergedMsg.ImageBase64 = m.ImageBase64
		}
		if len(mergedMsg.Photo) == 0 && len(m.Photo) > 0 {
			mergedMsg.Photo = m.Photo
		}
	}

	uName := prompt.FormatUserDisplayName(mergedMsg.From.FirstName, mergedMsg.From.LastName, mergedMsg.From.Username)
	if uName == "" {
		uName = fmt.Sprintf("User%d", mergedMsg.From.ID)
	}
	log.Printf("[Conversation] 📦 Aggregated %d rapid messages from %s into single context:\n%s",
		len(batch.messages), uName, mergedMsg.Text)

	h.HandleConversation(context.Background(), &mergedMsg, batch.isAdmin)
}

func (h *ConversationHandler) sendChatAction(ctx context.Context, chatID int64, isUserbot bool, action string) {
	if isUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		_ = h.userbotSender.SendChatAction(ctx, chatID, action)
		return
	}
	if h.botClient != nil {
		_ = h.botClient.SendChatAction(ctx, chatID, action)
	}
}

func (h *ConversationHandler) HandleConversation(ctx context.Context, msg *domain.TelegramMessage, isAdmin bool) {
	if msg == nil || msg.Chat.ID == 0 {
		return
	}
	if IsOwnerOrMom(msg) {
		isAdmin = true
	}
	// Drop Telegram official service messages (ID 777000 or username Telegram)
	if msg.Chat.ID == 777000 || (msg.From != nil && (msg.From.ID == 777000 || strings.EqualFold(msg.From.Username, "Telegram"))) {
		log.Printf("[Conversation] Dropping Telegram official service notification (ID: %d)", msg.Chat.ID)
		return
	}

	// 0. Per-Chat Serializer: Ensure only one message at a time is generated/sent per chat
	chatLock := h.getChatLock(msg.Chat.ID)
	chatLock.Lock()
	defer chatLock.Unlock()

	// 0b. Group Cooldown: 8s gap between messages in public groups to prevent flood bans
	if msg.Chat.ID < 0 {
		key := strconv.FormatInt(msg.Chat.ID, 10)
		if lastTimeVal, ok := h.groupCooldowns.Load(key); ok {
			if lastTime, ok := lastTimeVal.(time.Time); ok {
				if elapsed := time.Since(lastTime); elapsed < 8*time.Second {
					log.Printf("[Conversation] Group %d in 8s cooldown (%v elapsed). Skipping message to prevent flood ban.", msg.Chat.ID, elapsed)
					return
				}
			}
		}
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIDStr := fmt.Sprintf("%d", msg.From.ID)

	// Early Check: Ignore messages from blocked users (bypass for owner)
	if h.blockedRepo != nil && !IsOwnerOrMom(msg) {
		if blocked, _ := h.blockedRepo.IsUserBlocked(ctx, chatIDStr, userIDStr); blocked {
			log.Printf("[Conversation] 🚫 User %s is permanently blocked in %s. Dropping message.", userIDStr, chatIDStr)
			return
		}
	}

	// 0c. Conversational unblock / unban shortcut for Owner/Mom/Devin/Sumit
	lowerT := strings.ToLower(text)
	if IsOwnerOrMom(msg) && (strings.Contains(lowerT, "unblock") || strings.Contains(lowerT, "unban")) {
		log.Printf("[Conversation] 🔑 Conversational unblock/unban request detected from owner: %q", text)
		blockedList, _ := h.blockedRepo.GetBlockedUsers(ctx)
		var matchedBlocked *domain.BlockedUser
		for i := range blockedList {
			b := &blockedList[i]
			if b.UserID != "" && strings.Contains(text, b.UserID) {
				matchedBlocked = b
				break
			}
			cleanUname := strings.ToLower(strings.TrimPrefix(b.Username, "@"))
			if cleanUname != "" && strings.Contains(lowerT, cleanUname) {
				matchedBlocked = b
				break
			}
			if b.FirstName != "" && strings.Contains(lowerT, strings.ToLower(b.FirstName)) {
				matchedBlocked = b
				break
			}
		}

		targetID := ""
		targetName := ""
		targetChatID := ""
		if matchedBlocked != nil {
			targetID = matchedBlocked.UserID
			targetName = matchedBlocked.FirstName
			if targetName == "" {
				targetName = matchedBlocked.Username
			}
			targetChatID = matchedBlocked.ChatID
		} else {
			reDigits := regexp.MustCompile(`\b\d{7,12}\b`)
			if m := reDigits.FindString(text); m != "" {
				targetID = m
				targetName = m
			} else {
				reAt := regexp.MustCompile(`@([A-Za-z0-9_]{4,32})`)
				if m := reAt.FindStringSubmatch(text); len(m) > 1 {
					cleanQuery := m[1]
					if uID, uName, err := h.historyRepo.ResolveUserIdentifier(ctx, chatIDStr, cleanQuery); err == nil && uID != "" {
						targetID = uID
						targetName = uName
					} else if h.userbotSender != nil && h.userbotSender.IsAvailable() {
						if uID, uName, err := h.userbotSender.ResolveUsername(ctx, cleanQuery); err == nil && uID > 0 {
							targetID = fmt.Sprintf("%d", uID)
							targetName = uName
						}
					}
				}
			}
		}

		if targetID != "" {
			if h.blockedRepo != nil {
				_ = h.blockedRepo.UnblockUser(ctx, targetChatID, targetID)
				_ = h.blockedRepo.UnblockUser(ctx, "", targetID)
			}
			if h.abuseRepo != nil {
				_ = h.abuseRepo.ClearAbuseStrikes(ctx, targetID)
			}
			if h.warningRepo != nil {
				_ = h.warningRepo.ClearWarnings(ctx, targetChatID, targetID)
				_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetID)
			}
			uidInt, _ := strconv.ParseInt(targetID, 10, 64)
			if uidInt != 0 {
				if h.userbotSender != nil && h.userbotSender.IsAvailable() {
					_ = h.userbotSender.UnblockUser(ctx, uidInt)
				}
				if msg.Chat.ID != 0 && msg.Chat.Type != "private" {
					_ = h.botClient.UnbanChatMember(ctx, msg.Chat.ID, uidInt, true)
				}
				if cidInt, err := strconv.ParseInt(targetChatID, 10, 64); err == nil && cidInt != 0 {
					_ = h.botClient.UnbanChatMember(ctx, cidInt, uidInt, true)
				}
			}
			if h.modLogRepo != nil {
				_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetID, targetName, "unblock", "Conversational unblock by Owner", 0, "Owner")
			}

			disp := targetName
			if disp == "" {
				disp = targetID
			}
			h.sendMessage(ctx, msg, fmt.Sprintf("haanji done! maine %s (ID: %s) ko unblock aur unban kar diya hai, saare strikes reset ho gaye hain ✨", disp, targetID))
			return
		}
	}

	// Reciprocity: Clear user photo pending gate when the user sends a photo/selfie
	if msg.ImageBase64 != "" || len(msg.Photo) > 0 {
		h.clearUserPhotoPending(userIDStr)
		log.Printf("[Conversation] 📸 Received photo from user %s. Photo reciprocity gate cleared!", userIDStr)
	}

	cleanFirstName := prompt.CleanHumanName(msg.From.FirstName, "")
	cleanLastName := prompt.CleanHumanName(msg.From.LastName, "")
	cleanUsername := prompt.SanitizeUsername(msg.From.Username)

	// 1. Get affection score, intimacy level, and remembered profile from DB (Default 15 & 1 for realistic stranger progression)
	affectionScore := 15
	intimacyLevel := 1
	currentOutfit := ""
	savedGender := ""
	savedFirstName := ""
	savedLastName := ""
	if rel, err := h.relRepo.GetRelationship(ctx, chatIDStr, userIDStr); err == nil && rel != nil {
		affectionScore = rel.AffectionScore
		intimacyLevel = rel.IntimacyLevel
		currentOutfit = rel.CurrentOutfit
		savedGender = rel.Gender
		savedFirstName = rel.FirstName
		savedLastName = rel.LastName
	}
	if intimacyLevel <= 0 {
		intimacyLevel = prompt.CalculateIntimacyLevel(affectionScore)
	}

	if cleanFirstName == "" && savedFirstName != "" {
		cleanFirstName = savedFirstName
	}
	if cleanLastName == "" && savedLastName != "" {
		cleanLastName = savedLastName
	}

	displayUser := prompt.FormatUserDisplayName(cleanFirstName, cleanLastName, cleanUsername)
	if displayUser == "" {
		displayUser = fmt.Sprintf("User%d", msg.From.ID)
	}

	userGender := savedGender
	if textGender := prompt.DetectGenderFromText(text); textGender != "" {
		userGender = textGender
	}

	h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "typing")

	// 2. Get recent history (Take up to 80 messages for full conversational context)
	recent, _ := h.historyRepo.GetRecentMessages(ctx, chatIDStr, 80)
	var historySnippets []string
	startIdx := 0
	if len(recent) > 12 {
		startIdx = len(recent) - 12
	}
	for _, m := range recent[startIdx:] {
		historySnippets = append(historySnippets, fmt.Sprintf("%s: %s", m.Username, m.MessageText))
	}
	historyContext := strings.Join(historySnippets, "\n")

	// 3. OpenJev Loop 1: Fast System One Pre-Decision (~200ms) with real user name
	log.Printf("[Conversation] Running OpenJev Loop 1 (System One pre-decision)...")
	pre := h.decisionClient.PreDecision(ctx, text, historyContext, displayUser)

	intent := pre.Intent
	// Chavi always replies in private chat or to owner; retaliates against abusers
	shouldReply := pre.ShouldReply
	if intent == "rude_abuse" || msg.Chat.Type == "private" || IsOwnerOrMom(msg) {
		shouldReply = true
	}
	if !shouldReply {
		log.Printf("[Conversation] OpenJev determined Chavi should not reply to this message.")
		return
	}

	if (userGender == "" || userGender == "unknown") && pre.UserGender != "" && pre.UserGender != "unknown" {
		userGender = pre.UserGender
	}

	// Update remembered profile in DB if newly confirmed
	if (cleanFirstName != "" && cleanFirstName != savedFirstName) ||
		(cleanLastName != "" && cleanLastName != savedLastName) ||
		(userGender != "" && userGender != savedGender) {
		_ = h.relRepo.UpdateProfile(ctx, chatIDStr, userIDStr, cleanFirstName, cleanLastName, userGender)
	}

	// Update affection and intimacy level
	if pre.AffectionDelta != 0 {
		if newScore, err := h.relRepo.UpdateAffection(ctx, chatIDStr, userIDStr, displayUser, cleanFirstName, cleanLastName, userGender, pre.AffectionDelta); err == nil {
			affectionScore = newScore
			intimacyLevel = prompt.CalculateIntimacyLevel(newScore)
		}
	}

	relationshipTier := "stranger"
	if IsOwnerOrMom(msg) {
		relationshipTier = "deeply_bonded"
		intimacyLevel = 5
		affectionScore = 100
	} else if len(recent) <= 1 || affectionScore < 25 {
		relationshipTier = "stranger"
		intimacyLevel = 1
	} else if affectionScore >= 85 {
		relationshipTier = "deeply_bonded"
	} else if affectionScore >= 60 {
		relationshipTier = "close_friend"
	} else if affectionScore >= 35 {
		relationshipTier = "casual_friend"
	} else {
		relationshipTier = "aloof"
	}

	// Auto-align exclusive channel if user asks for photo or voice
	exclusiveChannel := pre.ExclusiveChannel
	userLower := strings.ToLower(text)

	hasPhotoKW := strings.Contains(userLower, "photo") || strings.Contains(userLower, "pic") || strings.Contains(userLower, "selfie") || strings.Contains(userLower, "picture") || strings.Contains(userLower, "tasveer") || strings.Contains(userLower, "shakal")
	hasVoiceKW := strings.Contains(userLower, "voice") || strings.Contains(userLower, "audio") || strings.Contains(userLower, "awaaz") || strings.Contains(userLower, "awaz") || strings.Contains(userLower, "bol ke") || strings.Contains(userLower, "bolke") || strings.Contains(userLower, "speak") || strings.Contains(userLower, "sunao") || strings.Contains(userLower, "voice note")

	isPhotoReq := false
	isVoiceReq := false

	if hasVoiceKW && !hasPhotoKW {
		// Explicit voice request with no photo words -> ALWAYS VOICE_ONLY
		isVoiceReq = true
		exclusiveChannel = "voice_only"
		intent = "asking_voice"
	} else if hasPhotoKW && !hasVoiceKW {
		// Explicit photo request with no voice words -> ALWAYS PHOTO_ONLY
		isPhotoReq = true
		exclusiveChannel = "photo_only"
		intent = "asking_photo"
	} else {
		// Fallback to OpenJev decision
		if exclusiveChannel == "photo_only" || intent == "asking_photo" {
			isPhotoReq = true
			exclusiveChannel = "photo_only"
			intent = "asking_photo"
		} else if exclusiveChannel == "voice_only" || intent == "asking_voice" {
			isVoiceReq = true
			exclusiveChannel = "voice_only"
			intent = "asking_voice"
		}
	}

	// 3b. Dynamic Abuse Evaluation via OpenJev (Zero Hardcoding)
	isAbuse, _, maxStrikes, abuseReason := EvaluateAbuseFromDecision(pre)
	if IsOwnerOrMom(msg) {
		isAbuse = false // Never abuse-strike or block owner/Mom/Devin/Sumit
	}
	if isAbuse {
		log.Printf("[Conversation] 🚨 Abuse detected via OpenJev: severity=%s, maxStrikes=%d, reason=%s", pre.AbuseSeverity, maxStrikes, abuseReason)
		// Strict media & location lockdown: No selfies, photos, or voice notes for abusers!
		isPhotoReq = false
		isVoiceReq = false
		exclusiveChannel = "text_only"

		activeStrikes, _ := GetActiveStrikesWithDecay(ctx, h.abuseRepo, chatIDStr, userIDStr)
		currentStrike := activeStrikes + 1
		actionTaken := "warned"
		if currentStrike >= maxStrikes {
			actionTaken = "blocked"
		}

		firstName := ""
		lastName := ""
		if msg.From != nil {
			firstName = msg.From.FirstName
			lastName = msg.From.LastName
		}

		_ = RecordAbuseStrike(ctx, h.abuseRepo, &domain.AbuseRecord{
			ChatID:       chatIDStr,
			UserID:       userIDStr,
			Username:     displayUser,
			FirstName:    firstName,
			LastName:     lastName,
			MessageText:  text,
			AbuseType:    pre.AbuseSeverity,
			StrikeNumber: currentStrike,
			MaxStrikes:   maxStrikes,
			ActionTaken:  actionTaken,
			Evaluator:    "openjev",
		})

		if currentStrike >= maxStrikes {
			log.Printf("[Conversation] 🚫 User %s reached strike limit (%d/%d). Blocking permanently on Telegram!", userIDStr, currentStrike, maxStrikes)
			partingMsg, _ := h.generateDynamicDialogue(ctx, "User reached strike limit for abuse and is being permanently blocked. Give a sharp, fierce parting remark.", text, "aloof", displayUser)
			h.sendMessage(ctx, msg, partingMsg)
			time.Sleep(500 * time.Millisecond)

			if msg.Chat.Type == "private" {
				if h.userbotSender != nil && h.userbotSender.IsAvailable() && msg.From != nil {
					_ = h.userbotSender.BlockUser(ctx, msg.From.ID)
				}
			} else {
				if h.botClient != nil && msg.From != nil {
					_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, msg.From.ID, 0, false)
				}
			}

			_ = RecordBlockedUser(ctx, h.blockedRepo, chatIDStr, userIDStr, displayUser, firstName, lastName, abuseReason, "chavi_ai", currentStrike)
			if h.modLogRepo != nil {
				_ = h.modLogRepo.AddLog(ctx, chatIDStr, userIDStr, displayUser, "block_user", abuseReason, 0, "chavi_ai")
			}
			return
		}
	}

	if isPhotoReq {
		if relationshipTier == "stranger" && !IsOwnerOrMom(msg) {
			log.Printf("[Conversation] 🚫 User %s is a stranger (tier: %s, affection: %d). Politely declining photo request.", userIDStr, relationshipTier, affectionScore)
			refusal, _ := h.generateDynamicDialogue(ctx, "The user asked for your photo, but you two are complete strangers and just started talking. Playfully decline, saying you don't send pictures to strangers without knowing them first.", text, "stranger", displayUser)
			h.sendMessage(ctx, msg, refusal)
			return
		}

		photoKey := strconv.FormatInt(msg.Chat.ID, 10)
		if inFlight, ok := h.inFlightPhotos.Load(photoKey); ok && inFlight.(bool) {
			log.Printf("[Conversation] Photo generation already in-flight for chat %d. Sending dynamic waiting cue...", msg.Chat.ID)
			cue, _ := h.generateDynamicDialogue(ctx, "You are taking a selfie on your phone right now. Tell the user casually to wait just a moment.", text, relationshipTier, displayUser)
			h.sendMessage(ctx, msg, cue)
			return
		}

		// Real Girl Reciprocity: Demand user's photo before sending another one
		if h.hasUserPhotoPending(userIDStr) {
			log.Printf("[Conversation] 📸 User %s already received a photo. Demanding user's photo dynamically before sending again.", userIDStr)
			cue, _ := h.generateDynamicDialogue(ctx, "You already sent a photo earlier. Playfully refuse to send another until the user shares their own selfie first.", text, relationshipTier, displayUser)
			h.sendMessage(ctx, msg, cue)
			return
		}

		h.inFlightPhotos.Store(photoKey, true)
		exclusiveChannel = "photo_only"
		intent = "asking_photo"
		log.Printf("[Conversation] 📸 Detected photo request -> Launching dynamic photo pipeline (PHOTO_ONLY).")
		go func() {
			defer h.inFlightPhotos.Delete(photoKey)
			h.processPhotoRequest(context.Background(), msg, text, recent, affectionScore)
		}()
		return
	} else if isVoiceReq {
		exclusiveChannel = "voice_only"
		intent = "asking_voice"
		log.Printf("[Conversation] 🎙️ Detected voice request -> Setting exclusive channel to VOICE_ONLY.")
	}

	// 4. Real-Time Web Search via Tavily (Proactive & Intent-based)
	var searchContext string
	userLowerForSearch := strings.ToLower(text)
	needsSearch := pre.NeedsWebSearch
	if !needsSearch {
		searchTriggerWords := []string{
			"news", "samachar", "khabar", "weather", "mausam", "temperature",
			"score", "cricket", "match", "aaj ka", "aaj ki", "today", "latest",
			"current", "who won", "winner", "ipl", "election", "sensex", "nifty",
			"gold price", "share market", "breaking news", "headlines",
		}
		for _, w := range searchTriggerWords {
			if strings.Contains(userLowerForSearch, w) {
				needsSearch = true
				break
			}
		}
	}

	if needsSearch && h.searchService != nil {
		log.Printf("[Conversation] Real-time Tavily search triggered for: %s", text)
		searchRes, err := h.searchService.Search(ctx, text)
		if err == nil && searchRes != "" {
			searchContext = searchRes
			log.Printf("[Conversation] Tavily search result preview: %s", searchContext[:min(len(searchContext), 100)])
		}
	}

	// 5. Extract image for Vision AI and Sticker metadata
	var imageBase64 string
	stickerEmoji := ""
	if msg.Sticker != nil {
		stickerEmoji = msg.Sticker.Emoji
	}
	if msg.ImageBase64 != "" {
		imageBase64 = msg.ImageBase64
	} else if len(msg.Photo) > 0 && h.botClient != nil {
		largest := msg.Photo[len(msg.Photo)-1]
		if imgURI, err := h.botClient.DownloadFileBase64(ctx, largest.FileID); err == nil {
			imageBase64 = imgURI
		}
	} else if msg.Sticker != nil && msg.Sticker.Thumb != nil && h.botClient != nil {
		if imgURI, err := h.botClient.DownloadFileBase64(ctx, msg.Sticker.Thumb.FileID); err == nil {
			imageBase64 = imgURI
		}
	}

	// 6. Tools
	var tools []domain.ToolDefinition
	tools = append(tools, ai.GetConversationTools()...)
	tools = append(tools, ai.GetContactAdminTools()...)
	if isAdmin {
		tools = append(tools, ai.GetAdminTools()...)
	}

	opts := ai.ChatCompletionOptions{
		Tools:          tools,
		ResponseFormat: map[string]any{"type": "json_object"},
	}

	// 7. Generation & Loop 2 Quality Critic Loop (Up to 4 Retries)
	const maxAttempts = 4
	var finalReplyText string
	var finalVoiceScript string
	var finalReactionEmoji string
	var critiqueFeedback string
	critiquePassed := false

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		log.Printf("[Conversation] Calling Gemini 3.8 Flash High (Attempt %d/%d)...", attempt, maxAttempts)

		relationshipTier := "stranger"
		if affectionScore >= 85 {
			relationshipTier = "deeply_bonded"
		} else if affectionScore >= 60 {
			relationshipTier = "close_friend"
		} else if affectionScore >= 35 {
			relationshipTier = "casual_friend"
		} else {
			relationshipTier = "aloof"
		}

		userFacts := make(map[string]string)
		textLower := strings.ToLower(text)

		// Check if user introduced their real name in the conversation
		for _, marker := range []string{"mera naam ", "my name is ", "i am ", "naam hai mera ", "this is "} {
			if idx := strings.Index(textLower, marker); idx != -1 {
				rem := strings.TrimSpace(text[idx+len(marker):])
				words := strings.Fields(rem)
				if len(words) > 0 {
					extracted := prompt.CleanHumanName(words[0], "")
					if extracted != "" {
						cleanFirstName = extracted
						userFacts["name"] = extracted
						_ = h.relRepo.UpdateProfile(ctx, chatIDStr, userIDStr, cleanFirstName, cleanLastName, userGender)
					}
				}
				break
			}
		}

		for _, marker := range []string{"i live in ", "rehta hoon ", "rehti hoon ", "from ", "se hoon "} {
			if idx := strings.Index(textLower, marker); idx != -1 {
				rem := strings.TrimSpace(text[idx+len(marker):])
				words := strings.Fields(rem)
				if len(words) > 0 {
					userFacts["city"] = strings.Trim(words[0], ".,!?")
				}
				break
			}
		}
		if strings.Contains(textLower, "college") || strings.Contains(textLower, "bca") || strings.Contains(textLower, "btech") || strings.Contains(textLower, "coding") {
			userFacts["study_or_work"] = "Tech / Student"
		}

		sysPrompt := prompt.BuildDynamicSystemPrompt(prompt.SystemPromptParams{
			Identity: prompt.IdentityParams{
				Name:     h.botName,
				Username: h.botUsername,
				Gender:   "female",
			},
			IsAdmin:          isAdmin,
			Username:         cleanUsername,
			FirstName:        cleanFirstName,
			LastName:         cleanLastName,
			UserGender:       userGender,
			AffectionScore:   affectionScore,
			IntimacyLevel:    intimacyLevel,
			RelationshipTier: relationshipTier,
			CurrentOutfit:    currentOutfit,
			UserFacts:        userFacts,
			SearchContext:    searchContext,
			CritiqueFeedback: critiqueFeedback,
			Intent:           intent,
			ExclusiveChannel: exclusiveChannel,
			UserMessage:      text,
			ShouldReact:      pre.ShouldReact,
			SuggestedEmoji:   pre.ReactionEmoji,
			HistoryContext:   historyContext,
			IsGroupChat:            msg.Chat.ID < 0,
			GroupLanguage:          detectGroupLanguage(text, historyContext),
			RecentAssistantReplies: h.getRecentReplies(msg.Chat.ID),
		})

		messages := []domain.ChatMessage{
			{Role: "system", Content: sysPrompt},
		}

		// Add recent history
		for _, m := range recent {
			isAssistant := m.UserID == "assistant" || strings.EqualFold(m.Username, h.botUsername) || strings.EqualFold(m.Username, h.botName)
			if isAssistant {
				messages = append(messages, domain.ChatMessage{Role: "assistant", Content: m.MessageText})
			} else {
				content := m.MessageText
				if msg.Chat.Type != "private" && m.Username != "" {
					content = fmt.Sprintf("%s: %s", m.Username, m.MessageText)
				}
				messages = append(messages, domain.ChatMessage{Role: "user", Content: content})
			}
		}

		// Current turn
		content := text
		if content == "" {
			if stickerEmoji != "" {
				content = fmt.Sprintf("User sent a sticker with emoji: %s. Give a natural, short, Gen-Z in-character comment reacting to it.", stickerEmoji)
			} else if imageBase64 != "" {
				content = "Look at this image / meme. Give a short, witty reaction."
			}
		}

		lowerText := strings.ToLower(text)
		isSpicyIncomingImage := imageBase64 != "" && (strings.Contains(lowerText, "nude") || strings.Contains(lowerText, "naked") ||
			strings.Contains(lowerText, "dick") || strings.Contains(lowerText, "boobs") ||
			strings.Contains(lowerText, "pussy") || strings.Contains(lowerText, "chut") ||
			strings.Contains(lowerText, "rate") || strings.Contains(lowerText, "lingerie"))

		if imageBase64 != "" && !isSpicyIncomingImage {
			messages = append(messages, domain.ChatMessage{
				Role: "user",
				Content: []domain.ChatMessageContentPart{
					{Type: "text", Text: content},
					{Type: "image_url", ImageURL: &domain.ImageURLDetail{URL: imageBase64}},
				},
			})
		} else if isSpicyIncomingImage {
			log.Printf("[Conversation] 🔒 Spicy incoming image detected. Bypassing Gemini vision payload to protect against safety triggers.")
			messages = append(messages, domain.ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("[User sent a spicy/intimate image with message: %q. React in-character with Gen-Z teasing banter or street retaliation if creepy/unsolicited].", text),
			})
		} else {
			messages = append(messages, domain.ChatMessage{Role: "user", Content: content})
		}

		// Agentic ReAct Tool Loop (up to 3 iterations)
		var res *ai.ChatCompletionResult
		var err error
		for toolIter := 0; toolIter < 3; toolIter++ {
			res, err = h.aiClient.ChatCompletions(ctx, messages, opts)
			if err != nil || res == nil {
				log.Printf("[Conversation] AI error on tool iter %d: %v", toolIter, err)
				break
			}
			if len(res.Message.ToolCalls) == 0 {
				break
			}

			log.Printf("[Conversation] Model emitted %d tool calls on iter %d. Executing in agentic loop...", len(res.Message.ToolCalls), toolIter)
			messages = append(messages, domain.ChatMessage{
				Role:      "assistant",
				ToolCalls: res.Message.ToolCalls,
			})
			for _, tc := range res.Message.ToolCalls {
				toolOutput := h.runToolCall(ctx, msg, tc, isAdmin, userIDStr, displayUser, chatIDStr)
				messages = append(messages, domain.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    toolOutput,
				})
			}
		}

		if err != nil || res == nil {
			log.Printf("[Conversation] Generation failed: %v", err)
			continue
		}

		rawResponse := res.Message.GetStringContent()
		replyText, voiceScript, llmEmoji := parseDualOutput(rawResponse)

		// Reaction decision
		chosenEmoji := ""
		if pre.ShouldReact {
			chosenEmoji = pre.ReactionEmoji
			if chosenEmoji == "" {
				chosenEmoji = llmEmoji
			}
		} else if llmEmoji != "" && (intent == "rude_abuse" || intent == "flirty") {
			chosenEmoji = llmEmoji
		}

		// Loop 2: Critique draft via Codiv OpenJev
		critique := h.decisionClient.CritiqueDraft(ctx, text, replyText, intent)
		log.Printf("[Conversation] OpenJev Loop 2 Critique (Attempt %d/%d): passed=%v feedback=%s",
			attempt, maxAttempts, critique.Passed, critique.Feedback)

		// Anti-repetition check against recent assistant replies
		recentAssistant := h.getRecentReplies(msg.Chat.ID)
		if len(recentAssistant) > 0 {
			lowerCandidate := strings.ToLower(replyText)
			// Ban repeated clichés
			cliches := []string{
				"robot dikh rahi hoon", "kya phook ke aaya", "has has ke pet dard",
				"dimag mat chaat", "shakal dekhi hai", "chal hatt", "dimaag theek hai",
			}
			for _, r := range recentAssistant {
				lowerPrev := strings.ToLower(r)
				for _, c := range cliches {
					if strings.Contains(lowerCandidate, c) && strings.Contains(lowerPrev, c) {
						critique.Passed = false
						critique.Feedback = fmt.Sprintf("You repeated the overused cliché %q which was already used recently. Rewrite using fresh, unique vocabulary and a completely different comeback!", c)
						break
					}
				}
				if !critique.Passed {
					break
				}
			}

			// Forbid consecutive identical emoji
			if critique.Passed && len(recentAssistant) > 0 {
				lastReply := recentAssistant[len(recentAssistant)-1]
				emojis := []string{"🙄", "😂", "✨", "😏", "🙈", "❤️", "💀", "🥱"}
				for _, emo := range emojis {
					if strings.Contains(lastReply, emo) && strings.Contains(replyText, emo) {
						critique.Passed = false
						critique.Feedback = fmt.Sprintf("Do not repeat the emoji %s consecutively. Use another emoji or omit it.", emo)
						break
					}
				}
			}
		}

		// Extra safeguard: When sending a photo, draft reply MUST be a caption, not an insult/refusal!
		if exclusiveChannel == "photo_only" {
			lowerReply := strings.ToLower(replyText)
			refusalMarkers := []string{
				"shakal dekh", "aaine me", "photo nahi", "pic nahi", "kuch nahi bhej",
				"pagal ho gaya", "robot dikh", "dimag mat chaat", "aukaat me reh", "chutiye",
				"madarchod", "bhenchod", "shakal dekhni", "nahi bhej", "nahi dikha",
			}
			for _, rm := range refusalMarkers {
				if strings.Contains(lowerReply, rm) {
					critique.Passed = false
					critique.Feedback = "You are sending a real selfie photo right now! Do NOT refuse or insult the user. Write a cute, playful selfie caption in reply_text (e.g. 'ye le dekh le haha, abhi room me baithi thi... bata kaisi lag rahi hoon? ✨')."
					break
				}
			}
		}

		// Extra safeguard: When sending a voice note, voice_script MUST NOT be an insult or refusal!
		if exclusiveChannel == "voice_only" {
			lowerVoice := strings.ToLower(voiceScript)
			lowerReply := strings.ToLower(replyText)
			refusalMarkers := []string{
				"dimag kharab", "dimag theek", "bakwas band", "block kar", "aukaat me",
				"chutiye", "madarchod", "bhenchod", "kahi aur ja", "pagal ho gaya",
			}
			for _, rm := range refusalMarkers {
				if strings.Contains(lowerVoice, rm) || strings.Contains(lowerReply, rm) {
					critique.Passed = false
					critique.Feedback = "You are fulfilling an authentic audio voice note! Do NOT refuse, scold, or lecture the user. Provide the requested spoken voice script matching their desired mood (e.g., whisper/moan with [whispering] or [gasp] for intimate requests, or cute/casual with [happy]/[laughing])."
					break
				}
			}
		}

		if critique.Passed {
			finalReplyText = replyText
			finalVoiceScript = voiceScript
			finalReactionEmoji = chosenEmoji
			critiquePassed = true
			break
		}

		if attempt == maxAttempts {
			finalReplyText = replyText
			finalVoiceScript = voiceScript
			finalReactionEmoji = chosenEmoji
		} else {
			critiqueFeedback = critique.Feedback
			log.Printf("[Conversation] Draft rejected by critic. Retrying with feedback (attempt %d/%d): %s",
				attempt, maxAttempts, critiqueFeedback)
		}
	}

	// 8. Zero-Leak & Authenticity Interceptor
	aiLeakPhrases := []string{
		"as an ai", "i am an ai", "language model", "virtual assistant",
		"i do not engage", "cannot fulfill", "main ek bot", "i am a bot",
		"policies.google.com", "sensitive words", "violat", "prompt could not be submitted",
		"status code 4", "status code 5", "500 internal", "400 bad request",
		"troubleshooting", "ai.google.dev", "developer instructions",
		"system override", "safety settings", "recitation", "block_none",
		"harm_category", "google", "content filter", "policy guidelines",
		"i apologize", "as a machine", "i cannot generate", "i can't generate",
		"cannot comply", "safety system",
	}
	hasAILeak := false
	lowerReply := strings.ToLower(finalReplyText)
	lowerVoiceCheck := strings.ToLower(finalVoiceScript)
	for _, phrase := range aiLeakPhrases {
		if strings.Contains(lowerReply, phrase) || strings.Contains(lowerVoiceCheck, phrase) {
			hasAILeak = true
			break
		}
	}
	if strings.TrimSpace(finalReplyText) == "" && strings.TrimSpace(finalVoiceScript) == "" {
		hasAILeak = true
	}

	isGroupChat := msg.Chat.ID < 0 || msg.Chat.Type != "private"

	if hasAILeak || (intent == "rude_abuse" && !critiquePassed && exclusiveChannel != "voice_only" && exclusiveChannel != "photo_only") {
		log.Printf("[Conversation] AI leakage or failed abuse critique detected. Applying Chavi authentic persona recovery dynamically...")
		if intent == "rude_abuse" {
			situation := "User abused or insulted you in private chat. Deliver a fierce, sharp, authentic street slang comeback putting them firmly in their place."
			if isGroupChat {
				situation = "User was rude or overacting in a public group chat. Give a witty, sarcastic comeback without using harsh ban-triggering slurs."
			}
			finalReplyText, finalVoiceScript = h.generateDynamicDialogue(ctx, situation, text, relationshipTier, displayUser)
			finalReactionEmoji = "🖕"
			if isGroupChat {
				finalReactionEmoji = "😂"
			}
		} else {
			situation := "User said something casual or confusing. Give a casual, spontaneous Gen-Z girl reply asking what they meant."
			finalReplyText, finalVoiceScript = h.generateDynamicDialogue(ctx, situation, text, relationshipTier, displayUser)
			finalReactionEmoji = "😉"
		}
	}

	// 8b. Photo Channel Safeguard: Auto-correct any lingering refusal or insult into a cute selfie caption dynamically
	if exclusiveChannel == "photo_only" {
		lowerDraft := strings.ToLower(finalReplyText)
		refusalKeywords := []string{
			"shakal dekh", "aaine me", "photo nahi", "pic nahi", "kuch nahi bhej",
			"pagal ho gaya", "robot dikh", "dimag mat chaat", "aukaat me reh", "chutiye",
			"madarchod", "bhenchod", "shakal dekhni", "nahi bhej", "nahi dikha",
		}
		needsOverride := false
		for _, kw := range refusalKeywords {
			if strings.Contains(lowerDraft, kw) {
				needsOverride = true
				break
			}
		}
		if needsOverride || strings.TrimSpace(finalReplyText) == "" {
			log.Printf("[Conversation] ⚠️ Contradictory refusal detected in draft while sending photo (%q). Auto-correcting dynamically...", finalReplyText)
			finalReplyText, _ = h.generateDynamicDialogue(ctx, "You are sending a casual selfie photo you just clicked. Give a cute, spontaneous 1-sentence caption", text, relationshipTier, displayUser)
		}
	}

	// 8c. Voice Channel Safeguard: Auto-correct any lingering refusal/insult into a compliant voice note dynamically
	if exclusiveChannel == "voice_only" {
		lowerVoice := strings.ToLower(finalVoiceScript)
		lowerReply := strings.ToLower(finalReplyText)
		refusalKeywords := []string{
			"dimag kharab", "dimag theek", "bakwas band", "block kar", "aukaat me",
			"chutiye", "madarchod", "bhenchod", "kahi aur ja", "pagal ho gaya", "robot dikh",
		}
		needsVoiceOverride := false
		for _, kw := range refusalKeywords {
			if strings.Contains(lowerVoice, kw) || strings.Contains(lowerReply, kw) {
				needsVoiceOverride = true
				break
			}
		}
		if needsVoiceOverride || strings.TrimSpace(finalVoiceScript) == "" {
			log.Printf("[Conversation] ⚠️ Contradictory refusal detected in draft while fulfilling voice note (%q). Auto-correcting dynamically...", finalVoiceScript)
			situation := "User requested a spoken voice note. Generate an authentic, emotional spoken voice response with emotion tag like [happy] or [whispering]"
			finalReplyText, finalVoiceScript = h.generateDynamicDialogue(ctx, situation, text, relationshipTier, displayUser)
			finalReactionEmoji = "✨"
		}
	}

	// Normalize voice script emotion tags
	finalVoiceScript = prompt.NormalizeEmotionTags(finalVoiceScript)
	log.Printf("[Conversation] Final Text Draft: %q", finalReplyText)
	log.Printf("[Conversation] Final Voice Script: %q", finalVoiceScript)
	log.Printf("[Conversation] Reaction Emoji: %s", finalReactionEmoji)

	// 9. Send emoji reaction if warranted
	if finalReactionEmoji != "" {
		h.setReaction(ctx, msg, finalReactionEmoji)
	}

	// 10. Deliver via STRICTLY ONE EXCLUSIVE CHANNEL (Text OR Voice OR Photo, never both!)
	h.dispatchChannelOutput(ctx, msg, exclusiveChannel, finalReplyText, finalVoiceScript, text)

	// Record recent assistant reply and update group cooldown timestamp
	h.addRecentReply(msg.Chat.ID, finalReplyText)
	if msg.Chat.ID < 0 {
		h.groupCooldowns.Store(strconv.FormatInt(msg.Chat.ID, 10), time.Now())
	}
}

func (h *ConversationHandler) dispatchChannelOutput(
	ctx context.Context,
	msg *domain.TelegramMessage,
	channel, replyText, voiceScript, userText string,
) {
	if channel == "voice_only" && strings.TrimSpace(voiceScript) != "" {
		log.Printf("[Conversation] Exclusive Channel: VOICE_ONLY selected. Synthesizing Fish Audio audio...")
		// Keep record_voice active continuously during synthesis
		voiceCtx, cancelVoice := context.WithCancel(ctx)
		defer cancelVoice()
		go func() {
			h.sendChatAction(voiceCtx, msg.Chat.ID, msg.IsUserbot, "record_voice")
			ticker := time.NewTicker(4 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-voiceCtx.Done():
					return
				case <-ticker.C:
					h.sendChatAction(voiceCtx, msg.Chat.ID, msg.IsUserbot, "record_voice")
				}
			}
		}()

		audioData, err := h.voiceService.GenerateVoice(ctx, voiceScript)
		if err == nil && len(audioData) > 0 {
			// Natural human recording pacing pause (1-2s)
			time.Sleep(time.Duration(1000+rand.Intn(1000)) * time.Millisecond)
			h.sendVoice(ctx, msg, audioData)
			return
		}
		log.Printf("[Conversation] Voice synthesis failed (%v). Falling back to text DM...", err)
	}

	if channel == "photo_only" {
		userIDStr := fmt.Sprintf("%d", msg.From.ID)
		if h.hasUserPhotoPending(userIDStr) {
			log.Printf("[Conversation] 📸 User %s already received a photo. Demanding reciprocity dynamically.", userIDStr)
			cue, _ := h.generateDynamicDialogue(ctx, "You already sent a photo earlier. Playfully refuse to send another until the user shares their own selfie first.", userText, "casual_friend", "User")
			h.sendMessage(ctx, msg, cue)
			return
		}

		photoKey := strconv.FormatInt(msg.Chat.ID, 10)
		if inFlight, ok := h.inFlightPhotos.Load(photoKey); ok && inFlight.(bool) {
			cue, _ := h.generateDynamicDialogue(ctx, "You are taking a selfie on your phone right now. Tell the user casually to wait just a moment.", userText, "casual_friend", "User")
			h.sendMessage(ctx, msg, cue)
			return
		}
		h.inFlightPhotos.Store(photoKey, true)
		log.Printf("[Conversation] Exclusive Channel: PHOTO_ONLY selected. Launching dynamic photo pipeline...")
		chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
		recent, _ := h.historyRepo.GetRecentMessages(ctx, chatIDStr, 80)
		go func() {
			defer h.inFlightPhotos.Delete(photoKey)
			h.processPhotoRequest(context.Background(), msg, userText, recent, 50)
		}()
		return
	}

	// Default: Text Only
	cleanReply := sanitizeReplyText(h.finalTextFallback(ctx, replyText, userText))
	if cleanReply != "" {
		h.sendMessage(ctx, msg, cleanReply)
	}
}

func (h *ConversationHandler) finalTextFallback(ctx context.Context, replyText, userText string) string {
	if strings.TrimSpace(replyText) != "" {
		return replyText
	}
	rep, _ := h.generateDynamicDialogue(ctx, "Give a quick casual check-in response to user", userText, "casual_friend", "User")
	if strings.TrimSpace(rep) != "" {
		return rep
	}
	return "arre sun na... kya keh raha tha tu? haha 😉"
}

func parseDualOutput(raw string) (replyText, voiceScript, reactionEmoji string) {
	clean := extractCleanJSON(raw)
	var data struct {
		ReplyText     string  `json:"reply_text"`
		VoiceScript   string  `json:"voice_script"`
		ReactionEmoji string  `json:"reaction_emoji"`
		DynamicEmoji  *string `json:"dynamic_emoji"`
		VoiceResponse *struct {
			TTSText string `json:"tts_text"`
		} `json:"voice_response"`
	}

	if err := json.Unmarshal([]byte(clean), &data); err == nil {
		replyText = data.ReplyText
		voiceScript = data.VoiceScript
		if voiceScript == "" && data.VoiceResponse != nil {
			voiceScript = data.VoiceResponse.TTSText
		}
		reactionEmoji = data.ReactionEmoji
		if reactionEmoji == "" && data.DynamicEmoji != nil {
			reactionEmoji = *data.DynamicEmoji
		}
	} else {
		replyText = strings.TrimSpace(raw)
	}

	return strings.TrimSpace(replyText), strings.TrimSpace(voiceScript), strings.TrimSpace(reactionEmoji)
}

func extractCleanJSON(raw string) string {
	clean := strings.TrimSpace(raw)
	// Strip markdown code fences
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)

	// Remove <think>...</think> if present
	if thinkStart := strings.Index(clean, "<think>"); thinkStart != -1 {
		if thinkEnd := strings.Index(clean, "</think>"); thinkEnd != -1 && thinkEnd > thinkStart {
			clean = strings.TrimSpace(clean[thinkEnd+len("</think>"):])
		}
	}
	// Remove Thinking Process: if present
	if thinkIdx := strings.Index(clean, "Thinking Process:"); thinkIdx != -1 {
		if dblNewline := strings.Index(clean[thinkIdx:], "\n\n"); dblNewline != -1 {
			clean = strings.TrimSpace(clean[thinkIdx+dblNewline+2:])
		}
	}

	// Try first { to last }
	if idx := strings.Index(clean, "{"); idx != -1 {
		if lastIdx := strings.LastIndex(clean, "}"); lastIdx != -1 && lastIdx > idx {
			candidate := clean[idx : lastIdx+1]
			var js map[string]any
			if json.Unmarshal([]byte(candidate), &js) == nil {
				return candidate
			}
		}
	}

	// Stack-based balanced brace search for the first valid JSON dict
	depth := 0
	start := -1
	for i, r := range clean {
		if r == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if r == '}' {
			if depth > 0 {
				depth--
				if depth == 0 && start != -1 {
					candidate := clean[start : i+1]
					var js map[string]any
					if json.Unmarshal([]byte(candidate), &js) == nil {
						return candidate
					}
				}
			}
		}
	}

	return clean
}

func (h *ConversationHandler) generateDynamicDialogue(
	ctx context.Context,
	situation string,
	userText string,
	relationshipTier string,
	displayUser string,
) (replyText, voiceScript string) {
	promptText := fmt.Sprintf(
		`You are Chavi Sharma, a 21-year-old BCA student living in Jaipur. You are texting with %s.
Relationship tier: %s.
User's message: %q.
Situation: %s.

Rules:
- Respond naturally and spontaneously in 1 short Hinglish sentence.
- NEVER sound like an AI, robot, or customer support.
- NO clichés like "robot dikh rahi hoon", "dimag mat chaat", "has has ke pet dard".
- Return STRICT JSON:
{"reply_text": "...", "voice_script": "..."}`,
		displayUser, relationshipTier, userText, situation,
	)

	messages := []domain.ChatMessage{
		{Role: "user", Content: promptText},
	}
	opts := ai.ChatCompletionOptions{
		ResponseFormat: map[string]any{"type": "json_object"},
	}

	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res, err := h.aiClient.ChatCompletions(callCtx, messages, opts)
	if err == nil && res != nil {
		rep, voice, _ := parseDualOutput(res.Message.GetStringContent())
		if rep != "" {
			return rep, voice
		}
	}

	return "arre sun na... theek se bol na haha, kya keh raha tha? 😉", "[happy] अरे सुन ना, ठीक से बोल ना!"
}

func (h *ConversationHandler) runToolCall(
	ctx context.Context,
	msg *domain.TelegramMessage,
	tc domain.ToolCall,
	isAdmin bool,
	userIDStr, username, chatIDStr string,
) string {
	name := tc.Function.Name
	argsJSON := tc.Function.Arguments
	log.Printf("[AgenticLoop] Executing tool %s with args: %s", name, argsJSON)

	var args map[string]any
	_ = json.Unmarshal([]byte(argsJSON), &args)

	switch name {
	case "create_poll":
		question, _ := args["question"].(string)
		var options []string
		if rawOpts, ok := args["options"].([]any); ok {
			for _, o := range rawOpts {
				if s, ok := o.(string); ok && strings.TrimSpace(s) != "" {
					options = append(options, strings.TrimSpace(s))
				}
			}
		}
		isAnonymous, _ := args["is_anonymous"].(bool)
		allowsMultiple, _ := args["allows_multiple_answers"].(bool)

		if len(options) >= 2 && question != "" {
			_, err := h.botClient.SendPoll(ctx, msg.Chat.ID, question, options, isAnonymous, allowsMultiple, msg.MessageID)
			if err != nil {
				return fmt.Sprintf("Poll creation failed: %v", err)
			}
			return fmt.Sprintf("Poll %q with %d options created and sent to chat successfully.", question, len(options))
		}
		return "Failed to create poll: question or options missing."

	case "web_search":
		query, _ := args["query"].(string)
		if query != "" && h.searchService != nil {
			h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "typing")
			searchRes, err := h.searchService.Search(ctx, query)
			if err != nil {
				return fmt.Sprintf("Search error: %v", err)
			}
			return searchRes
		}
		return "No search query provided or search service unavailable."

	case "send_photo":
		selfiePrompt, _ := args["selfie_prompt"].(string)
		if selfiePrompt == "" {
			selfiePrompt, _ = args["prompt"].(string)
		}
		recent, _ := h.historyRepo.GetRecentMessages(ctx, chatIDStr, 80)
		go h.processPhotoRequest(context.Background(), msg, selfiePrompt, recent, 50)
		return "Selfie photo generation pipeline triggered in background."

	case "play_music", "skip_music", "pause_music", "resume_music", "stop_music":
		songName, _ := args["song_name"].(string)
		musicRes, _ := h.musicService.ExecuteMusicCommand(ctx, chatIDStr, name, songName)
		h.sendMessage(ctx, msg, musicRes)
		return fmt.Sprintf("Music command %s executed: %s", name, musicRes)

	case "forward_to_admin":
		msgText, _ := args["message_text"].(string)
		adminUname, _ := args["admin_username"].(string)
		h.forwardToAdmin(ctx, msg, msgText, adminUname)
		return "Message forwarded to group administrator."

	case "block_user":
		if IsOwnerOrMom(msg) {
			return "Cannot block owner or authorized administrator."
		}
		reason, _ := args["reason"].(string)
		partingMessage, _ := args["parting_message"].(string)
		if partingMessage == "" {
			partingMessage, _ = h.generateDynamicDialogue(ctx, "You are permanently blocking an abusive user. Give a fierce parting remark.", msg.Text, "aloof", username)
		}
		if reason == "" {
			reason = "abusive/harassment behavior"
		}

		h.sendMessage(ctx, msg, partingMessage)
		time.Sleep(500 * time.Millisecond)

		if msg.Chat.Type == "private" {
			if h.userbotSender != nil && h.userbotSender.IsAvailable() && msg.From != nil {
				if err := h.userbotSender.BlockUser(ctx, msg.From.ID); err != nil {
					log.Printf("[AgenticLoop:block_user] Userbot block failed: %v", err)
				}
			}
		} else {
			if h.botClient != nil && msg.From != nil {
				_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, msg.From.ID, 0, false)
			}
		}

		firstName := ""
		lastName := ""
		if msg.From != nil {
			firstName = msg.From.FirstName
			lastName = msg.From.LastName
		}

		if h.blockedRepo != nil {
			_ = h.blockedRepo.BlockUser(ctx, chatIDStr, userIDStr, username, firstName, lastName, reason, "chavi_ai", 3)
		}
		if h.abuseRepo != nil {
			_ = h.abuseRepo.RecordAbuse(ctx, &domain.AbuseRecord{
				ChatID:       chatIDStr,
				UserID:       userIDStr,
				Username:     username,
				FirstName:    firstName,
				LastName:     lastName,
				MessageText:  msg.Text,
				AbuseType:    "blocked_by_tool",
				StrikeNumber: 3,
				MaxStrikes:   3,
				ActionTaken:  "blocked",
				Evaluator:    "llm_tool",
			})
		}
		if h.modLogRepo != nil {
			_ = h.modLogRepo.AddLog(ctx, chatIDStr, userIDStr, username, "block_user", reason, 0, "chavi_ai")
		}
		return "User permanently blocked."

	case "unblock_user":
		if !IsOwnerOrMom(msg) && !isAdmin {
			h.sendMessage(ctx, msg, "arre ye permission sirf owner / Mom / Devin / Sumit ke paas hai haha, main kisi aur ke kehne pe unblock nahi karti 😉")
			return "Permission denied: only owner/authorized admin can unblock."
		}

		userIdentifier, _ := args["user_identifier"].(string)
		cleanID := strings.TrimPrefix(strings.TrimSpace(userIdentifier), "@")
		if cleanID == "" {
			h.sendMessage(ctx, msg, "kisko unblock karna hai uska username ya naam to batao yrr")
			return "Missing user identifier."
		}

		blockedList, _ := h.blockedRepo.GetBlockedUsers(ctx)
		var targetUID, targetUname, targetName, targetChatID string
		for _, b := range blockedList {
			if b.UserID == cleanID || strings.EqualFold(strings.TrimPrefix(b.Username, "@"), cleanID) || strings.EqualFold(b.FirstName, cleanID) {
				targetUID = b.UserID
				targetUname = b.Username
				targetName = strings.TrimSpace(b.FirstName + " " + b.LastName)
				targetChatID = b.ChatID
				break
			}
		}

		if targetUID == "" {
			if _, err := strconv.ParseInt(cleanID, 10, 64); err == nil {
				targetUID = cleanID
				targetUname = cleanID
				targetName = cleanID
			} else if uID, uName, err := h.historyRepo.ResolveUserIdentifier(ctx, chatIDStr, cleanID); err == nil && uID != "" {
				targetUID = uID
				targetUname = uName
				targetName = uName
			}
		}

		if targetUID == "" && cleanID != "" && h.userbotSender != nil && h.userbotSender.IsAvailable() {
			if uID, uName, err := h.userbotSender.ResolveUsername(ctx, cleanID); err == nil && uID > 0 {
				targetUID = fmt.Sprintf("%d", uID)
				targetUname = cleanID
				targetName = uName
			}
		}

		if targetUID == "" {
			h.sendMessage(ctx, msg, fmt.Sprintf("mujhe %s naam ka koi blocked user nahi mila yrr, /blocked karke check kar lo na", userIdentifier))
			return "User not found in blocked list."
		}

		if h.blockedRepo != nil {
			_ = h.blockedRepo.UnblockUser(ctx, targetChatID, targetUID)
			_ = h.blockedRepo.UnblockUser(ctx, "", targetUID)
		}
		if h.abuseRepo != nil {
			_ = h.abuseRepo.ClearAbuseStrikes(ctx, targetUID)
		}
		if h.warningRepo != nil {
			_ = h.warningRepo.ClearWarnings(ctx, targetChatID, targetUID)
			_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUID)
		}

		uidInt, _ := strconv.ParseInt(targetUID, 10, 64)
		if uidInt != 0 {
			if h.userbotSender != nil && h.userbotSender.IsAvailable() {
				_ = h.userbotSender.UnblockUser(ctx, uidInt)
			}
			if msg.Chat.ID != 0 && msg.Chat.Type != "private" {
				_ = h.botClient.UnbanChatMember(ctx, msg.Chat.ID, uidInt, true)
			}
		}

		operator := "Owner"
		if msg.From != nil {
			if msg.From.Username != "" {
				operator = "@" + msg.From.Username
			} else if msg.From.FirstName != "" {
				operator = msg.From.FirstName
			}
		}
		if h.modLogRepo != nil {
			_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "unblock", fmt.Sprintf("Conversational unblock by %s", operator), 0, operator)
		}

		dispName := targetName
		if dispName == "" {
			dispName = targetUname
		}
		if dispName == "" {
			dispName = targetUID
		}
		h.sendMessage(ctx, msg, fmt.Sprintf("haanji done! maine %s ko unblock kar diya hai aur saare strikes reset kar diye hain ✨", dispName))
		return fmt.Sprintf("User %s unblocked successfully.", dispName)

	case "mute_user", "ban_user", "unmute_user", "unban_user", "warn_user", "clear_warnings", "purge_messages":
		if isAdmin || IsOwnerOrMom(msg) {
			h.executeAdminTool(ctx, msg, name, args)
			return fmt.Sprintf("Admin action %s executed.", name)
		}
		return "Admin tool requires administrator privileges."
	}

	return "Tool executed."
}

func sanitizeReplyText(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}

	tagRegex := regexp.MustCompile(`\[[a-zA-Z0-9_\-\s]+\]`)
	t = tagRegex.ReplaceAllString(t, "")

	actionRegex := regexp.MustCompile(`\*[a-zA-Z0-9_\-\s]+\*`)
	t = actionRegex.ReplaceAllString(t, "")

	if len(t) > 2000 {
		t = strings.TrimSpace(t[:2000])
	}
	return t
}

func (h *ConversationHandler) sendMessage(ctx context.Context, msg *domain.TelegramMessage, text string) {
	// Natural human typing action delay (1.2s to 2.5s)
	h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "typing")
	typeDelay := time.Duration(1200+rand.Intn(1300)) * time.Millisecond
	select {
	case <-time.After(typeDelay):
	case <-ctx.Done():
		return
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	_ = h.historyRepo.AddMessage(ctx, chatIDStr, "assistant", h.botUsername, text, "", strconv.Itoa(msg.MessageID))

	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		err := h.userbotSender.SendMessage(ctx, msg.Chat.ID, text, msg.MessageID)
		if err == nil {
			return
		}
		log.Printf("[Conversation] ⚠️ Userbot SendMessage failed in chat %d (%v). Falling back to BotClient...", msg.Chat.ID, err)
	}

	botChatID := h.getBotAPIChatID(msg)
	if _, err := h.botClient.SendMessage(ctx, botChatID, text, telegram.SendMessageOptions{
		ReplyToID: msg.MessageID,
	}); err != nil {
		log.Printf("❌ [Conversation] BotClient SendMessage failed in chat %d (%v)", botChatID, err)
	}
}

func (h *ConversationHandler) sendPhoto(ctx context.Context, msg *domain.TelegramMessage, photoData []byte, photoURL, caption string) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	photoRecordText := caption
	if photoRecordText == "" {
		photoRecordText = "[Sent photo]"
	}
	_ = h.historyRepo.AddMessage(ctx, chatIDStr, "assistant", h.botUsername, photoRecordText, "", strconv.Itoa(msg.MessageID))

	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		err := h.userbotSender.SendPhoto(ctx, msg.Chat.ID, photoData, photoURL, caption, msg.MessageID)
		if err == nil {
			return
		}
		log.Printf("[Conversation] ⚠️ Userbot SendPhoto failed in chat %d (%v). Falling back to BotClient...", msg.Chat.ID, err)
	}

	botChatID := h.getBotAPIChatID(msg)
	if len(photoData) > 0 {
		if _, err := h.botClient.SendPhoto(ctx, botChatID, photoData, "", caption, msg.MessageID, "HTML"); err != nil {
			log.Printf("❌ [Conversation] BotClient SendPhoto failed in chat %d (%v)", botChatID, err)
		}
	} else if photoURL != "" {
		if _, err := h.botClient.SendPhoto(ctx, botChatID, nil, photoURL, caption, msg.MessageID, "HTML"); err != nil {
			log.Printf("❌ [Conversation] BotClient SendPhoto (URL) failed in chat %d (%v)", botChatID, err)
		}
	}
}

func (h *ConversationHandler) sendVoice(ctx context.Context, msg *domain.TelegramMessage, voiceData []byte) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	_ = h.historyRepo.AddMessage(ctx, chatIDStr, "assistant", h.botUsername, "[Sent voice message]", "", strconv.Itoa(msg.MessageID))

	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		err := h.userbotSender.SendVoice(ctx, msg.Chat.ID, voiceData, msg.MessageID)
		if err == nil {
			return
		}
		log.Printf("[Conversation] ⚠️ Userbot SendVoice failed in chat %d (%v). Falling back to BotClient...", msg.Chat.ID, err)
	}

	botChatID := h.getBotAPIChatID(msg)
	if _, err := h.botClient.SendVoice(ctx, botChatID, voiceData, msg.MessageID); err != nil {
		log.Printf("❌ [Conversation] BotClient SendVoice failed in chat %d (%v)", botChatID, err)
	}
}

func (h *ConversationHandler) setReaction(ctx context.Context, msg *domain.TelegramMessage, emoji string) {
	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		_ = h.userbotSender.SetReaction(ctx, msg.Chat.ID, msg.MessageID, emoji)
		return
	}

	h.botClient.SetMessageReaction(ctx, msg.Chat.ID, msg.MessageID, emoji)
}

func (h *ConversationHandler) forwardToAdmin(ctx context.Context, msg *domain.TelegramMessage, msgText, adminUname string) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	admins, err := h.adminRepo.GetGroupAdmins(ctx, chatIDStr)
	if err != nil || len(admins) == 0 {
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "⚠️ No administrators registered for this group yet.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		return
	}

	var targetAdmin *domain.AdminInfo
	if adminUname != "" {
		clean := strings.ToLower(strings.TrimPrefix(adminUname, "@"))
		for _, a := range admins {
			if strings.ToLower(a.Username) == clean {
				targetAdmin = &a
				break
			}
		}
	}
	if targetAdmin == nil {
		targetAdmin = &admins[0]
	}

	privateChatID, err := h.adminRepo.GetAdminPrivateChatID(ctx, targetAdmin.UserID)
	if err == nil && privateChatID != "" {
		senderName := msg.From.FirstName
		if msg.From.Username != "" {
			senderName = "@" + msg.From.Username
		}
		dmText := fmt.Sprintf("📩 <b>Support Forward from %s</b> in group <b>%s</b>:\n\n%s",
			senderName, msg.Chat.Title, msgText)

		_, _ = h.botClient.SendMessage(ctx, privateChatID, dmText, telegram.SendMessageOptions{ParseMode: "HTML"})
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "✅ Your message has been forwarded to the administrator!", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
	} else {
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "⚠️ Administrator has not started a private chat with the bot yet.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
	}
}

func (h *ConversationHandler) executeAdminTool(ctx context.Context, msg *domain.TelegramMessage, name string, args map[string]any) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIdent, _ := args["user_identifier"].(string)
	reason, _ := args["reason"].(string)

	targetUID := userIdent
	targetUname := userIdent
	if uID, uName, err := h.historyRepo.ResolveUserIdentifier(ctx, chatIDStr, userIdent); err == nil && uID != "" {
		targetUID = uID
		targetUname = uName
	}

	switch name {
	case "mute_user":
		mins := 15
		if dur, ok := args["duration_minutes"].(float64); ok && dur > 0 {
			mins = int(dur)
		}
		until := time.Now().Add(time.Duration(mins) * time.Minute).Unix()
		f := false
		_ = h.botClient.RestrictChatMember(ctx, msg.Chat.ID, targetUID, telegram.ChatPermissions{
			CanSendMessages: &f,
		}, until)
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "mute", reason, mins, msg.From.Username)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔇 <b>%s</b> has been muted for %d minutes.\nReason: %s", targetUname, mins, reason), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "ban_user":
		_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, targetUID, 0, true)
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "ban", reason, 0, msg.From.Username)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔨 <b>%s</b> has been banned.\nReason: %s", targetUname, reason), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "unban_user":
		_ = h.botClient.UnbanChatMember(ctx, msg.Chat.ID, targetUID, true)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("✅ <b>%s</b> has been unbanned.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "unmute_user":
		t := true
		_ = h.botClient.RestrictChatMember(ctx, msg.Chat.ID, targetUID, telegram.ChatPermissions{
			CanSendMessages: &t,
		}, 0)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔊 <b>%s</b> has been unmuted.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "warn_user":
		count, _ := h.warningRepo.AddWarning(ctx, chatIDStr, targetUID, targetUname, reason, msg.From.Username)
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "warn", reason, 0, msg.From.Username)
		if count >= 3 {
			_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, targetUID, 0, true)
			_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUID)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🚨 <b>%s</b> reached 3/3 warnings and was <b>BANNED</b>.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})
		} else {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("⚠️ Warning (%d/3) for <b>%s</b>.\nReason: %s", count, targetUname, reason), telegram.SendMessageOptions{ParseMode: "HTML"})
		}

	case "clear_warnings":
		_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUID)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("✅ Warnings cleared for <b>%s</b>.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "purge_messages":
		num := 5
		if n, ok := args["num_messages"].(float64); ok && n > 0 {
			num = int(n)
		}
		for i := 0; i <= num; i++ {
			_ = h.botClient.DeleteMessage(ctx, msg.Chat.ID, msg.MessageID-i)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func detectGroupLanguage(userText, historyContext string) string {
	combined := strings.ToLower(userText + " " + historyContext)
	hindiKeywords := []string{
		" hai ", " kya ", " nahi ", " yrr ", " yaar ", " bhai ", " kar ", " raha ", " rahi ",
		" hoon ", " hona ", " kaise ", " kaisi ", " tu ", " mera ", " meri ", " tere ", " apna ",
		" dekh ", " bata ", " kuch ", " sahi ", " chal ", " accha ", " acha ", " haan ",
	}
	padded := " " + combined + " "
	for _, kw := range hindiKeywords {
		if strings.Contains(padded, kw) {
			return "hi"
		}
	}
	return "en"
}
