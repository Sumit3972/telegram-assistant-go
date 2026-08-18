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
	botClient *telegram.BotClient,
	aiClient *ai.Client,
) *Moderator {
	imageKey := cfg.ImageAPIKey
	if imageKey == "" {
		imageKey = cfg.AIAPIKey
	}
	imgService := media.NewImageService(cfg.GeminiImageAPIURL, imageKey, cfg.ImageModel)
	voiceService := media.NewVoiceService(cfg.FishAudioAPIKey)
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
		botClient:     botClient,
		aiClient:      aiClient,
		imageService:  imgService,
		voiceService:  voiceService,
		searchService: searchService,
		musicService:  musicService,

		karmaHandler:   NewKarmaHandler(karmaRepo, botClient),
		captchaHandler: NewCaptchaHandler(captchaRepo, botClient),
		mentionHandler: NewAdminMentionHandler(adminRepo, mentionRepo, historyRepo, botClient),
		commandHandler: NewCommandHandler(groupRepo, adminRepo, warningRepo, modLogRepo, historyRepo, karmaRepo, shipRepo, aiClient, botClient),
		autoModHandler: NewAutoModHandler(groupRepo, warningRepo, modLogRepo, botClient),
		convHandler: NewConversationHandler(
			aiClient, imgService, voiceService, searchService, musicService,
			historyRepo, relRepo, groupRepo, adminRepo, warningRepo, modLogRepo,
			botClient, cfg.MyPersonalName, cfg.MyPersonalUsername, cfg.AIAPIKey,
		),
		bootTime: time.Now().Add(-30 * time.Second).Unix(),
	}
}

func (m *Moderator) SetUserbotSender(s domain.UserbotSender) {
	m.convHandler.SetUserbotSender(s)
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
	if text != "" && !strings.HasPrefix(text, "/") {
		uName := msg.From.Username
		if uName == "" {
			uName = msg.From.FirstName
		}
		_ = m.historyRepo.AddMessage(
			ctx,
			chatIDStr,
			userIDStr,
			uName,
			text,
			strconv.Itoa(msg.MessageID),
			"",
		)
	}

	// 4. Handle Private Chat (DM)
	if msg.Chat.Type == "private" {
		if strings.HasPrefix(text, "/start") {
			m.commandHandler.HandleCommand(ctx, msg, false)
			return
		}
		// Treat private chat as 1-on-1 AI conversation in parallel
		go m.convHandler.HandleConversation(context.Background(), msg, false)
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

	// 7. Check Slash Commands
	if strings.HasPrefix(text, "/") {
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

	// 11. Check if talking to Janvi
	if m.isTalkingToBot(msg, text) {
		go m.convHandler.HandleConversation(context.Background(), msg, isAdmin)
	}
}

func (m *Moderator) isTalkingToBot(msg *domain.TelegramMessage, text string) bool {
	if text == "" {
		return false
	}

	lowerText := strings.ToLower(text)
	botUname := strings.ToLower(strings.TrimPrefix(m.cfg.MyPersonalUsername, "@"))
	botName := strings.ToLower(m.cfg.MyPersonalName)

	// 1. Check mention of username (@Janvi3976 or username string)
	if botUname != "" && (strings.Contains(lowerText, "@"+botUname) || strings.Contains(lowerText, botUname)) {
		return true
	}

	// 2. Check mention of name (Janvi, Chavi, and common phonetic variations)
	nameTriggers := []string{
		botName,
		"janvi", "jaanvi", "jhanvi", "zanvi", "janu", "jaanu",
		"chavi", "chhavi", "chabi", "chaavi", "chhabbi", "chhabee",
	}
	for _, n := range nameTriggers {
		if n != "" && strings.Contains(lowerText, n) {
			return true
		}
	}

	// 3. Check reply to her message
	if msg.ReplyToMessage != nil {
		// A. Check by From user metadata if present
		if msg.ReplyToMessage.From != nil {
			rUser := msg.ReplyToMessage.From
			if (rUser.IsBot && strings.EqualFold(rUser.Username, botUname)) ||
				fmt.Sprintf("%d", rUser.ID) == m.cfg.MyPersonalUserID ||
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
