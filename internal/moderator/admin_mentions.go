package moderator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/telegram"
)

type AdminMentionHandler struct {
	adminRepo   domain.AdminRepository
	mentionRepo domain.MentionRepository
	historyRepo domain.HistoryRepository
	botClient   *telegram.BotClient
}

func NewAdminMentionHandler(
	adminRepo domain.AdminRepository,
	mentionRepo domain.MentionRepository,
	historyRepo domain.HistoryRepository,
	botClient *telegram.BotClient,
) *AdminMentionHandler {
	return &AdminMentionHandler{
		adminRepo:   adminRepo,
		mentionRepo: mentionRepo,
		historyRepo: historyRepo,
		botClient:   botClient,
	}
}

func (h *AdminMentionHandler) HandleAdminMentions(
	ctx context.Context,
	msg *domain.TelegramMessage,
	mentionedAdmins []domain.AdminInfo,
) {
	if len(mentionedAdmins) == 0 {
		return
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	senderIDStr := fmt.Sprintf("%d", msg.From.ID)

	// Rate limit admin mentions (max 5 per 24 hours)
	_ = h.mentionRepo.RecordMentionLog(ctx, chatIDStr, senderIDStr)
	count, _ := h.mentionRepo.GetRecentMentionCount(ctx, chatIDStr, senderIDStr, 86400)
	if count > 5 {
		warnText := fmt.Sprintf("⚠️ @%s, you have reached the 24-hour limit for pinging administrators.", msg.From.Username)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, warnText, telegram.SendMessageOptions{
			ReplyToID: msg.MessageID,
		})
		return
	}

	// Check if admin is away (no messages in last 10 minutes)
	tenMinsAgo := time.Now().Add(-10 * time.Minute).Unix()
	var awayAdmins []domain.AdminInfo

	for _, admin := range mentionedAdmins {
		active, err := h.historyRepo.IsAdminActiveSince(ctx, chatIDStr, admin.UserID, tenMinsAgo)
		if err == nil && !active {
			awayAdmins = append(awayAdmins, admin)
		}
	}

	if len(awayAdmins) == 0 {
		return
	}

	targetAdmin := awayAdmins[0]
	name := targetAdmin.Username
	if name == "" {
		name = "Admin"
	} else {
		name = "@" + name
	}

	replyText := fmt.Sprintf("🔔 %s is currently not available in chat.\n\n💬 <b>Reply to this message</b> with what you need, and I will forward it directly to their private DM!", name)

	sentMsg, err := h.botClient.SendMessage(ctx, msg.Chat.ID, replyText, telegram.SendMessageOptions{
		ParseMode: "HTML",
		ReplyToID: msg.MessageID,
	})
	if err != nil || sentMsg == nil {
		return
	}

	sentMsgIDStr := fmt.Sprintf("%d", sentMsg.MessageID)
	_ = h.mentionRepo.SetMentionSession(ctx, chatIDStr, sentMsgIDStr, targetAdmin.UserID, senderIDStr)
}

func (h *AdminMentionHandler) HandleAdminAwayReply(ctx context.Context, msg *domain.TelegramMessage) bool {
	if msg == nil || msg.ReplyToMessage == nil {
		return false
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	replyMsgIDStr := fmt.Sprintf("%d", msg.ReplyToMessage.MessageID)

	session, err := h.mentionRepo.GetMentionSession(ctx, chatIDStr, replyMsgIDStr)
	if err != nil || session == nil {
		return false
	}

	// Forward message to admin DM
	privateChatID, err := h.adminRepo.GetAdminPrivateChatID(ctx, session.AdminUserID)
	if err == nil && privateChatID != "" {
		senderName := msg.From.FirstName
		if msg.From.Username != "" {
			senderName = "@" + msg.From.Username
		}

		dmText := fmt.Sprintf("📩 <b>New message forwarded from %s</b> in group <b>%s</b>:\n\n%s",
			senderName, msg.Chat.Title, msg.Text)

		_, _ = h.botClient.SendMessage(ctx, privateChatID, dmText, telegram.SendMessageOptions{
			ParseMode: "HTML",
		})

		confirmText := fmt.Sprintf("✅ Your message has been forwarded to the administrator's private DM!")
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, confirmText, telegram.SendMessageOptions{
			ReplyToID: msg.MessageID,
		})
	} else {
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "⚠️ Administrator has not started a private chat with the bot yet.", telegram.SendMessageOptions{
			ReplyToID: msg.MessageID,
		})
	}

	_ = h.mentionRepo.DeleteMentionSession(ctx, chatIDStr, replyMsgIDStr)
	return true
}

func (h *AdminMentionHandler) DetectAdminMentions(ctx context.Context, chatID string, text string) ([]domain.AdminInfo, error) {
	admins, err := h.adminRepo.GetGroupAdmins(ctx, chatID)
	if err != nil || len(admins) == 0 {
		return nil, err
	}

	lowerText := strings.ToLower(text)
	var matched []domain.AdminInfo

	for _, a := range admins {
		if a.Username != "" {
			uname := strings.ToLower(a.Username)
			if strings.Contains(lowerText, "@"+uname) || strings.Contains(lowerText, uname) {
				matched = append(matched, a)
			}
		}
	}
	return matched, nil
}
