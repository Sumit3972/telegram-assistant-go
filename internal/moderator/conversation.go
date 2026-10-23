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
	botClient        *telegram.BotClient
	userbotSender    domain.UserbotSender
	botName          string
	botUsername      string
	fallbackKey      string
	debounceDuration time.Duration
	maxBatchWait     time.Duration

	batchMu sync.Mutex
	batches map[string]*messageBatch
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

	uName := prompt.SanitizeUsername(mergedMsg.From.Username)
	if uName == "" {
		uName = prompt.SanitizeInterlocutorName(mergedMsg.From.FirstName)
	}
	if uName == "" {
		uName = fmt.Sprintf("ID:%d", mergedMsg.From.ID)
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
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIDStr := fmt.Sprintf("%d", msg.From.ID)
	cleanUsername := prompt.SanitizeUsername(msg.From.Username)
	cleanFirstName := prompt.SanitizeInterlocutorName(msg.From.FirstName)
	if cleanFirstName == "" && msg.From.LastName != "" {
		cleanFirstName = prompt.SanitizeInterlocutorName(msg.From.LastName)
	}

	displayUser := cleanUsername
	if displayUser == "" {
		displayUser = cleanFirstName
	}
	if displayUser == "" {
		displayUser = fmt.Sprintf("user_%d", msg.From.ID)
	}

	h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "typing")

	// 1. Get affection score
	affectionScore := 50
	if rel, err := h.relRepo.GetRelationship(ctx, chatIDStr, userIDStr); err == nil && rel != nil {
		affectionScore = rel.AffectionScore
	}

	// 2. Get recent history
	recent, _ := h.historyRepo.GetRecentMessages(ctx, chatIDStr, 20)
	var historySnippets []string
	for _, m := range recent {
		if len(historySnippets) >= 4 {
			break
		}
		historySnippets = append(historySnippets, fmt.Sprintf("%s: %s", m.Username, m.MessageText))
	}
	historyContext := strings.Join(historySnippets, "\n")

	// 3. OpenJev Loop 1: Fast System One Pre-Decision (~200ms)
	log.Printf("[Conversation] Running OpenJev Loop 1 (System One pre-decision)...")
	pre := h.decisionClient.PreDecision(ctx, text, historyContext)

	intent := pre.Intent
	// Chavi always retaliates against abusers with raw street curses; never stays silent!
	shouldReply := pre.ShouldReply
	if intent == "rude_abuse" {
		shouldReply = true
	}
	if !shouldReply {
		log.Printf("[Conversation] OpenJev determined Chavi should not reply to this message.")
		return
	}

	// Update affection
	if pre.AffectionDelta != 0 {
		_, _ = h.relRepo.UpdateAffection(ctx, chatIDStr, userIDStr, displayUser, pre.AffectionDelta)
	}

	// Auto-align exclusive channel if user asks for photo or voice
	exclusiveChannel := pre.ExclusiveChannel
	userLower := strings.ToLower(text)
	isPhotoReq := intent == "asking_photo" || (
		(strings.Contains(userLower, "photo") || strings.Contains(userLower, "pic") || strings.Contains(userLower, "selfie") || strings.Contains(userLower, "picture")) &&
			(strings.Contains(userLower, "bhej") || strings.Contains(userLower, "send") || strings.Contains(userLower, "dikha") || strings.Contains(userLower, "share") || strings.Contains(userLower, "click") || strings.Contains(userLower, "de") || strings.Contains(userLower, "bejo") || strings.Contains(userLower, "bhejo") || strings.Contains(userLower, "dikhao") || strings.Contains(userLower, "dikhana") || strings.Contains(userLower, "naa") || strings.Contains(userLower, "na")))
	isVoiceReq := intent == "asking_voice" || (
		(strings.Contains(userLower, "voice") || strings.Contains(userLower, "audio") || strings.Contains(userLower, "awaaz") || strings.Contains(userLower, "bol ke") || strings.Contains(userLower, "speak")) &&
			(strings.Contains(userLower, "bhej") || strings.Contains(userLower, "send") || strings.Contains(userLower, "sunao") || strings.Contains(userLower, "karo") || strings.Contains(userLower, "note") || strings.Contains(userLower, "bhejo") || strings.Contains(userLower, "bejo")))

	if isPhotoReq {
		exclusiveChannel = "photo_only"
		intent = "asking_photo"
		log.Printf("[Conversation] 📸 Detected photo request -> Setting exclusive channel to PHOTO_ONLY.")
	} else if isVoiceReq {
		exclusiveChannel = "voice_only"
		intent = "asking_voice"
		log.Printf("[Conversation] 🎙️ Detected voice request -> Setting exclusive channel to VOICE_ONLY.")
	}

	// 4. Real-Time Web Search via Tavily (if needed)
	var searchContext string
	if pre.NeedsWebSearch && h.searchService != nil {
		log.Printf("[Conversation] Real-time search requested for: %s", text)
		searchRes, err := h.searchService.Search(ctx, text)
		if err == nil && searchRes != "" {
			searchContext = searchRes
			log.Printf("[Conversation] Tavily search result preview: %s", searchContext[:min(len(searchContext), 100)])
		}
	}

	// 5. Extract image for Vision AI
	var imageBase64 string
	if msg.ImageBase64 != "" {
		imageBase64 = msg.ImageBase64
	} else if len(msg.Photo) > 0 && h.botClient != nil {
		largest := msg.Photo[len(msg.Photo)-1]
		if imgURI, err := h.botClient.DownloadFileBase64(ctx, largest.FileID); err == nil {
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
					extracted := prompt.SanitizeInterlocutorName(words[0])
					if extracted != "" {
						cleanFirstName = extracted
						userFacts["name"] = extracted
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
			AffectionScore:   affectionScore,
			RelationshipTier: relationshipTier,
			UserFacts:        userFacts,
			SearchContext:    searchContext,
			CritiqueFeedback: critiqueFeedback,
			Intent:           intent,
			ExclusiveChannel: exclusiveChannel,
			UserMessage:      text,
			ShouldReact:      pre.ShouldReact,
			SuggestedEmoji:   pre.ReactionEmoji,
			HistoryContext:   historyContext,
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
		if content == "" && imageBase64 != "" {
			content = "Look at this image"
		}
		if imageBase64 != "" {
			messages = append(messages, domain.ChatMessage{
				Role: "user",
				Content: []domain.ChatMessageContentPart{
					{Type: "text", Text: content},
					{Type: "image_url", ImageURL: &domain.ImageURLDetail{URL: imageBase64}},
				},
			})
		} else {
			messages = append(messages, domain.ChatMessage{Role: "user", Content: content})
		}

		res, err := h.aiClient.ChatCompletions(ctx, messages, opts)
		if err != nil || res == nil {
			log.Printf("[Conversation] AI error: %v", err)
			continue
		}

		// Handle Tool Calls immediately if triggered by model
		if len(res.Message.ToolCalls) > 0 {
			for _, tc := range res.Message.ToolCalls {
				h.executeToolCall(ctx, msg, tc, isAdmin, messages, userIDStr, displayUser, chatIDStr)
			}
			return
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

	// 8. AI Leakage & Character Safeguard Interceptor
	aiLeakPhrases := []string{"as an ai", "i am an ai", "language model", "virtual assistant", "i do not engage", "cannot fulfill", "main ek bot", "i am a bot"}
	hasAILeak := false
	lowerReply := strings.ToLower(finalReplyText)
	for _, phrase := range aiLeakPhrases {
		if strings.Contains(lowerReply, phrase) {
			hasAILeak = true
			break
		}
	}

	if hasAILeak || (intent == "rude_abuse" && !critiquePassed) {
		log.Printf("[Conversation] AI leakage or failed abuse critique detected. Applying Chavi authentic persona recovery...")
		if intent == "rude_abuse" {
			retaliations := []struct{ text, voice string }{
				{"chup kar chutiye, dimaag mat chaat mera. shakal dekhi hai apni aaine me? nikal 🖕", "[angry] चुप कर चूतिए... दिमाग मत चाट मेरा! शक्ल देखी है अपनी आईने में? निकल यहाँ से!"},
				{"apni aukaat me rehke baat kar madarchod, samjha na? apna ghatiya dimaag kahin aur jaake chala 🖕", "[angry] अपनी औकात में रहके बात कर, समझा ना? निकल यहाँ से!"},
				{"teri jaison ki aukaat do kaudi ki nahi hoti aur chale aate hain DM me bhaukne. nikal bhenchod 🖕", "[annoyed] तेरी जैसों की औकात दो कौड़ी की नहीं होती और चले आते हैं डी एम में भौंकने... निकल!"},
			}
			chosen := retaliations[rand.Intn(len(retaliations))]
			finalReplyText = chosen.text
			finalVoiceScript = chosen.voice
			finalReactionEmoji = "🖕"
		} else {
			finalReplyText = "kya pagal ho gaya h kya yrr? dimag theek h tera? 🙄"
			finalVoiceScript = "[sigh] क्या पागल हो गया है क्या यार? दिमाग ठीक है तेरा?"
			finalReactionEmoji = "🙄"
		}
	}

	// 8b. Photo Channel Safeguard: Auto-correct any lingering refusal or insult into a cute selfie caption
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
			log.Printf("[Conversation] ⚠️ Contradictory refusal detected in draft while sending photo (%q). Auto-correcting to cute photo caption...", finalReplyText)
			cuteCaptions := []string{
				"ye le dekh le haha, abhi room me baithi thi... bata kaisi lag rahi hoon? ✨",
				"le bhej di photo, zyada mat ghoorna ab 😏",
				"ye abhi clg practical ke baad li thi, kaisa lag raha h outfit?",
				"le photo, khush ab? haha ✨",
				"ye le meri photo, bata kaisa lag raha h?",
			}
			finalReplyText = cuteCaptions[rand.Intn(len(cuteCaptions))]
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
}

func (h *ConversationHandler) dispatchChannelOutput(
	ctx context.Context,
	msg *domain.TelegramMessage,
	channel, replyText, voiceScript, userText string,
) {
	if channel == "voice_only" && strings.TrimSpace(voiceScript) != "" {
		log.Printf("[Conversation] Exclusive Channel: VOICE_ONLY selected. Synthesizing Fish Audio audio...")
		h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "record_voice")
		audioData, err := h.voiceService.GenerateVoice(ctx, voiceScript)
		if err == nil && len(audioData) > 0 {
			h.sendVoice(ctx, msg, audioData)
			return
		}
		log.Printf("[Conversation] Voice synthesis failed (%v). Falling back to text DM...", err)
	}

	if channel == "photo_only" {
		log.Printf("[Conversation] Exclusive Channel: PHOTO_ONLY selected. Generating selfie via ModelScope...")

		// 1. Immediately send text companion message so the user receives an instant reply in chat!
		cleanReply := sanitizeReplyText(finalTextFallback(replyText, userText))
		if cleanReply != "" {
			h.sendMessage(ctx, msg, cleanReply)
		}

		// 2. Launch background photo generation tailored to the user's prompt
		go h.generateAndSendPhoto(context.Background(), msg, userText, replyText)
		return
	}

	// Default: Text Only
	cleanReply := sanitizeReplyText(finalTextFallback(replyText, userText))
	if cleanReply != "" {
		h.sendMessage(ctx, msg, cleanReply)
	}
}

func finalTextFallback(replyText, userText string) string {
	if strings.TrimSpace(replyText) != "" {
		return replyText
	}
	return "kya scene hai yrr? batao na"
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

func (h *ConversationHandler) executeToolCall(
	ctx context.Context,
	msg *domain.TelegramMessage,
	tc domain.ToolCall,
	isAdmin bool,
	history []domain.ChatMessage,
	userIDStr, username, chatIDStr string,
) {
	name := tc.Function.Name
	argsJSON := tc.Function.Arguments
	log.Printf("[ToolCall] Executing tool %s with args: %s", name, argsJSON)

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
				log.Printf("[ToolCall:create_poll] Failed: %v", err)
			}
		}

	case "web_search":
		query, _ := args["query"].(string)
		if query != "" {
			h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "typing")
			searchRes, err := h.searchService.Search(ctx, query)
			if err != nil {
				searchRes = fmt.Sprintf("Search error: %v", err)
			}

			toolMessages := append(history,
				domain.ChatMessage{
					Role:      "assistant",
					ToolCalls: []domain.ToolCall{tc},
				},
				domain.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       name,
					Content:    searchRes,
				},
			)

			finalRes, err := h.aiClient.ChatCompletions(ctx, toolMessages, ai.ChatCompletionOptions{})
			if err == nil && finalRes != nil {
				reply, _, _ := parseDualOutput(finalRes.Message.GetStringContent())
				h.sendMessage(ctx, msg, reply)
			}
		}

	case "send_photo":
		selfiePrompt, _ := args["selfie_prompt"].(string)
		replyText, _ := args["reply_text"].(string)
		go h.generateAndSendPhoto(context.Background(), msg, selfiePrompt, replyText)

	case "play_music", "skip_music", "pause_music", "resume_music", "stop_music":
		songName, _ := args["song_name"].(string)
		musicRes, _ := h.musicService.ExecuteMusicCommand(ctx, chatIDStr, name, songName)
		h.sendMessage(ctx, msg, musicRes)

	case "forward_to_admin":
		msgText, _ := args["message_text"].(string)
		adminUname, _ := args["admin_username"].(string)
		h.forwardToAdmin(ctx, msg, msgText, adminUname)

	case "mute_user", "ban_user", "unmute_user", "unban_user", "warn_user", "clear_warnings", "purge_messages":
		if isAdmin {
			h.executeAdminTool(ctx, msg, name, args)
		}
	}
}

func (h *ConversationHandler) generateAndSendPhoto(ctx context.Context, msg *domain.TelegramMessage, rawPrompt, replyText string) {
	h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "upload_photo")
	log.Printf("[Conversation] Generating photo via ModelScope with user prompt: %s", rawPrompt)

	img, err := h.imageService.GenerateImage(ctx, rawPrompt)
	if err != nil || img == nil {
		log.Printf("[Conversation] Failed to generate image: %v", err)
		return
	}

	caption := "ye lo meri photo ✨"
	if len(img.Data) > 0 {
		h.sendPhoto(ctx, msg, img.Data, "", caption)
	} else if img.URL != "" {
		h.sendPhoto(ctx, msg, nil, img.URL, caption)
	}
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
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	_ = h.historyRepo.AddMessage(ctx, chatIDStr, "assistant", h.botUsername, text, "", strconv.Itoa(msg.MessageID))

	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		err := h.userbotSender.SendMessage(ctx, msg.Chat.ID, text, msg.MessageID)
		if err == nil {
			return
		}
	}

	_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, text, telegram.SendMessageOptions{
		ReplyToID: msg.MessageID,
	})
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
	}

	if len(photoData) > 0 {
		_, _ = h.botClient.SendPhoto(ctx, msg.Chat.ID, photoData, "", caption, msg.MessageID, "HTML")
	} else if photoURL != "" {
		_, _ = h.botClient.SendPhoto(ctx, msg.Chat.ID, nil, photoURL, caption, msg.MessageID, "HTML")
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
	}

	_, _ = h.botClient.SendVoice(ctx, msg.Chat.ID, voiceData, msg.MessageID)
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
