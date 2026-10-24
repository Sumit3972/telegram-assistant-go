package moderator

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"telegram-ai-assistant/internal/ai"
	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/media"
	"telegram-ai-assistant/internal/prompt"
	"telegram-ai-assistant/internal/telegram"
)

type Moderator struct {
	cfg         *config.Config
	groupRepo   domain.GroupRepository
	adminRepo   domain.AdminRepository
	warningRepo domain.WarningRepository
	modLogRepo  domain.ModerationLogRepository
	historyRepo domain.HistoryRepository
	karmaRepo   domain.KarmaRepository
	captchaRepo domain.CaptchaRepository
	mentionRepo domain.MentionRepository
	relRepo     domain.RelationshipRepository
	shipRepo    domain.ShipRepository
	apiKeyRepo  domain.ApiKeyRepository
	perfRepo    domain.PerformanceRepository
	blockedRepo domain.BlockedUserRepository
	abuseRepo   domain.AbuseRecordRepository

	botClient     *telegram.BotClient
	aiClient      *ai.Client
	imageService  *media.ImageService
	voiceService  *media.VoiceService
	searchService *media.SearchService
	musicService  *media.MusicService

	karmaHandler   *KarmaHandler
	captchaHandler *CaptchaHandler
	mentionHandler *AdminMentionHandler
	commandHandler *CommandHandler
	autoModHandler *AutoModHandler
	convHandler    *ConversationHandler

	bootTime int64
}

func NewModerator(
	cfg *config.Config,
	groupRepo domain.GroupRepository,
	adminRepo domain.AdminRepository,
	warningRepo domain.WarningRepository,
	modLogRepo domain.ModerationLogRepository,
	historyRepo domain.HistoryRepository,
	karmaRepo domain.KarmaRepository,
	captchaRepo domain.CaptchaRepository,
	mentionRepo domain.MentionRepository,
	relRepo domain.RelationshipRepository,
	shipRepo domain.ShipRepository,
	apiKeyRepo domain.ApiKeyRepository,
	perfRepo domain.PerformanceRepository,
	blockedRepo domain.BlockedUserRepository,
	abuseRepo domain.AbuseRecordRepository,
	botClient *telegram.BotClient,
	aiClient *ai.Client,
) *Moderator {
	imgService := media.NewImageService(cfg.ModelScopeAPIKey, cfg.ModelScopeAPIURL, cfg.ModelScopeModel)
	if agy := aiClient.GetAntigravity(); agy != nil && agy.IsConfigured() {
		imgService.SetAntigravityClient(agy)
	}
	voiceService := media.NewVoiceService(cfg.FishAudioAPIKey, cfg.FishAudioRefID)
	searchService := media.NewSearchService(cfg.TavilyAPIKey)
	musicService := media.NewMusicService(cfg.MusicBotURL, cfg.MusicBotSecret)

	return &Moderator{
		cfg:           cfg,
		groupRepo:     groupRepo,
		adminRepo:     adminRepo,
		warningRepo:   warningRepo,
		modLogRepo:    modLogRepo,
		historyRepo:   historyRepo,
		karmaRepo:     karmaRepo,
		captchaRepo:   captchaRepo,
		mentionRepo:   mentionRepo,
		relRepo:       relRepo,
		shipRepo:      shipRepo,
		apiKeyRepo:    apiKeyRepo,
		perfRepo:      perfRepo,
		blockedRepo:   blockedRepo,
		abuseRepo:     abuseRepo,
		botClient:     botClient,
		aiClient:      aiClient,
		imageService:  imgService,
		voiceService:  voiceService,
		searchService: searchService,
		musicService:  musicService,

		karmaHandler:   NewKarmaHandler(karmaRepo, botClient),
		captchaHandler: NewCaptchaHandler(captchaRepo, botClient),
		mentionHandler: NewAdminMentionHandler(adminRepo, mentionRepo, historyRepo, botClient),
		commandHandler: NewCommandHandler(groupRepo, adminRepo, warningRepo, modLogRepo, historyRepo, karmaRepo, shipRepo, blockedRepo, abuseRepo, aiClient, botClient, cfg.MyPersonalName),
		autoModHandler: NewAutoModHandler(groupRepo, warningRepo, modLogRepo, botClient),
		convHandler: NewConversationHandler(
			aiClient, imgService, voiceService, searchService, musicService,
			historyRepo, relRepo, groupRepo, adminRepo, warningRepo, modLogRepo,
			blockedRepo, abuseRepo,
			botClient, cfg.MyPersonalName, cfg.MyPersonalUsername, cfg.AIAPIKey, cfg.CodivAPIKey,
			time.Duration(cfg.DebounceDurationMs)*time.Millisecond,
		),
		bootTime: time.Now().Add(-30 * time.Second).Unix(),
	}
}

func (m *Moderator) SetUserbotSender(s domain.UserbotSender) {
	m.convHandler.SetUserbotSender(s)
	m.commandHandler.SetUserbotSender(s)
}

func (m *Moderator) HandleUpdate(ctx context.Context, update *domain.TelegramUpdate) {
	if update == nil {
		return
	}

	// 1. Handle Callback Query (e.g. CAPTCHA buttons)
	if update.CallbackQuery != nil {
		m.captchaHandler.HandleCallbackQuery(ctx, update.CallbackQuery)
		return
	}

	msg := update.Message
	if msg == nil {
		return
	}

	// Ignore messages from other bots
	if msg.From != nil && msg.From.IsBot {
		return
	}

	// Skip stale messages
	if msg.Date > 0 && msg.Date < m.bootTime {
		return
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIDStr := fmt.Sprintf("%d", msg.From.ID)

	// Early Check: Drop updates from blocked users
	if m.blockedRepo != nil {
		if blocked, _ := m.blockedRepo.IsUserBlocked(ctx, chatIDStr, userIDStr); blocked {
			log.Printf("[Moderator] 🚫 Dropping update from blocked user %s in chat %s", userIDStr, chatIDStr)
			return
		}
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}

	// 2. Handle group migration
	if msg.MigrateToChatID != 0 {
		newChatIDStr := fmt.Sprintf("%d", msg.MigrateToChatID)
		_ = m.groupRepo.MigrateChatID(ctx, chatIDStr, newChatIDStr)
		log.Printf("[Moderator] Migrated chat %s to supergroup %s", chatIDStr, newChatIDStr)
		return
	}

	// 3. Cache incoming message in history (for both private and group chats)
	recordText := text
	if recordText == "" && msg.Sticker != nil {
		recordText = fmt.Sprintf("[Sticker: %s]", msg.Sticker.Emoji)
	}
	if recordText != "" && !strings.HasPrefix(recordText, "/") {
		uName := prompt.FormatUserDisplayName(msg.From.FirstName, msg.From.LastName, msg.From.Username)
		if uName == "" {
			uName = fmt.Sprintf("User%d", msg.From.ID)
		}
		_ = m.historyRepo.AddMessage(
			ctx,
			chatIDStr,
			userIDStr,
			uName,
			recordText,
			strconv.Itoa(msg.MessageID),
			"",
		)
	}

	// 4. Handle Private Chat (DM)
	if msg.Chat.Type == "private" {
		if strings.HasPrefix(text, "/") || strings.HasPrefix(text, ".") {
			if handled := m.commandHandler.HandleCommand(ctx, msg, true); handled {
				return
			}
		}
		// Buffer rapid consecutive messages so AI replies to the complete context at once
		m.convHandler.EnqueueConversation(context.Background(), msg, false)
		return
	}

	// 5. Auto-discover group
	title := msg.Chat.Title
	if title == "" {
		title = "Telegram Group"
	}
	_ = m.groupRepo.CreateOrUpdateGroup(ctx, chatIDStr, title)

	// 6. Handle New Chat Members
	if len(msg.NewChatMembers) > 0 {
		for _, member := range msg.NewChatMembers {
			if !member.IsBot {
				m.captchaHandler.HandleNewMember(ctx, msg.Chat.ID, member)
			}
		}
		return
	}

	// Check if user is admin
	isAdmin := m.isUserAdmin(ctx, msg.Chat.ID, msg.From.ID)
	if isAdmin && msg.From.Username != "" {
		_ = m.adminRepo.RegisterAdmin(ctx, chatIDStr, userIDStr, msg.From.Username, "")
	}

	// 7. Check Commands (/ or .)
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, ".") {
		if handled := m.commandHandler.HandleCommand(ctx, msg, isAdmin); handled {
			return
		}
	}

	// 8. Parse Karma (+/-)
	if text != "" && msg.ReplyToMessage != nil {
		m.karmaHandler.HandleKarma(ctx, msg)
	}

	// 9. Check Admin Mentions & Away alerts
	if text != "" && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && msg.ReplyToMessage.From.IsBot {
		if m.mentionHandler.HandleAdminAwayReply(ctx, msg) {
			return
		}
	}

	if text != "" && !isAdmin {
		mentionedAdmins, err := m.mentionHandler.DetectAdminMentions(ctx, chatIDStr, text)
		if err == nil && len(mentionedAdmins) > 0 {
			m.mentionHandler.HandleAdminMentions(ctx, msg, mentionedAdmins)
			return
		}
	}

	// 10. Auto-Moderation (Profanity filter)
	if m.autoModHandler.HandleAutoModeration(ctx, msg, isAdmin) {
		return
	}

	// 11. Check if talking to Chavi (either direct mention/reply, or active ongoing multi-message thought)
	triggered := m.isTalkingToBot(msg, text) || (msg.From != nil && m.convHandler.HasActiveBatch(msg.Chat.ID, msg.From.ID))
	if triggered {
		log.Printf("[Moderator] Group message triggered AI conversation in chat %s: %q", chatIDStr, text)
		m.convHandler.EnqueueConversation(context.Background(), msg, isAdmin)
	} else {
		log.Printf("[Moderator] Group message skipped (not directed at bot) in chat %s: %q", chatIDStr, text)
	}
}

func (m *Moderator) isTalkingToBot(msg *domain.TelegramMessage, text string) bool {
	// 0. Strict filter: Ignore all bots and automated services
	if msg.From != nil {
		uLower := strings.ToLower(msg.From.Username)
		if msg.From.IsBot || strings.HasSuffix(uLower, "bot") || uLower == "missrose_bot" {
			return false
		}
	}

	trimmed := strings.TrimSpace(text)

	// 0b. In groups, ignore admin and bot commands (!warn, !mute, !ban, #notes, /commands)
	if strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/") {
		return false
	}

	// 0c. Ignore moderation notification strings
	tLower := strings.ToLower(trimmed)
	if strings.Contains(tLower, "banned chavi") || strings.Contains(tLower, "muted chavi") ||
		strings.Contains(tLower, "kicked chavi") || strings.Contains(tLower, "warned chavi") ||
		strings.Contains(tLower, "another one bites the dust") {
		return false
	}

	// In groups, ignore empty messages with no text and no photos/media to prevent meaningless AI triggers
	hasMedia := msg.ImageBase64 != "" || len(msg.Photo) > 0 || msg.Sticker != nil
	if trimmed == "" && !hasMedia {
		return false
	}

	botUname := strings.ToLower(strings.TrimPrefix(m.cfg.MyPersonalUsername, "@"))
	botName := strings.ToLower(m.cfg.MyPersonalName)

	// 1. Check direct reply to bot's message (e.g. text reply or image sent in reply to bot)
	if msg.ReplyToMessage != nil {
		// A. Check by From user metadata if present
		if msg.ReplyToMessage.From != nil {
			rUser := msg.ReplyToMessage.From
			if (rUser.IsBot && strings.EqualFold(rUser.Username, botUname)) ||
				fmt.Sprintf("%d", rUser.ID) == m.cfg.MyPersonalUserID ||
				(rUser.ID != 0 && (fmt.Sprintf("%d", rUser.ID) == "6009675121" || fmt.Sprintf("%d", rUser.ID) == "8542441463")) ||
				(botUname != "" && strings.EqualFold(rUser.Username, botUname)) {
				return true
			}
		}

		// B. Check by ReplyToMessage.MessageID in chat_history database
		if msg.ReplyToMessage.MessageID > 0 {
			chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
			replyIDStr := strconv.Itoa(msg.ReplyToMessage.MessageID)
			if isBotMsg, err := m.historyRepo.IsMessageFromBotOrAssistant(
				context.Background(), chatIDStr, replyIDStr, m.cfg.MyPersonalUserID, m.cfg.MyPersonalUsername,
			); err == nil && isBotMsg {
				return true
			}
			// Also check fallback with fixed ID 6009675121
			if isBotMsg, err := m.historyRepo.IsMessageFromBotOrAssistant(
				context.Background(), chatIDStr, replyIDStr, "6009675121", "Chvai396",
			); err == nil && isBotMsg {
				return true
			}
		}
	}

	// In group chats, if not a direct reply to bot, must have text with name/mention
	if text == "" {
		return false
	}

	lowerText := strings.ToLower(text)

	// 2. Check mention of usernames (Chvai396 and variations)
	userNames := []string{
		botUname,
		"chvai396", "chavi396", "chvai",
	}
	for _, u := range userNames {
		if u != "" && (strings.Contains(lowerText, "@"+u) || strings.Contains(lowerText, u)) {
			return true
		}
	}

	// 3. Check mention of name (Chavi Sharma and phonetic variations)
	nameTriggers := []string{
		botName,
		"chavi sharma", "chavi", "chhavi", "chabi", "chaavi", "chhabbi", "chhabee",
	}
	for _, n := range nameTriggers {
		if n != "" && strings.Contains(lowerText, n) {
			return true
		}
	}

	return false
}

func (m *Moderator) isUserAdmin(ctx context.Context, chatID int64, userID int64) bool {
	member, err := m.botClient.GetChatMember(ctx, chatID, userID)
	if err != nil || member == nil {
		return false
	}
	return member.Status == "creator" || member.Status == "administrator"
}
