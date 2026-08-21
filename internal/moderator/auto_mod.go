package moderator

import (
	"context"
	"fmt"
	"strings"

	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/prompt"
	"telegram-ai-assistant/internal/telegram"
)

type AutoModHandler struct {
	groupRepo   domain.GroupRepository
	warningRepo domain.WarningRepository
	modLogRepo  domain.ModerationLogRepository
	botClient   *telegram.BotClient
}

func NewAutoModHandler(
	groupRepo domain.GroupRepository,
	warningRepo domain.WarningRepository,
	modLogRepo domain.ModerationLogRepository,
	botClient *telegram.BotClient,
) *AutoModHandler {
	return &AutoModHandler{
		groupRepo:   groupRepo,
		warningRepo: warningRepo,
		modLogRepo:  modLogRepo,
		botClient:   botClient,
	}
}

func (h *AutoModHandler) HandleAutoModeration(ctx context.Context, msg *domain.TelegramMessage, isAdmin bool) bool {
	if isAdmin || msg.From == nil || msg.From.IsBot {
		return false
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		return false
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	group, err := h.groupRepo.GetGroup(ctx, chatIDStr)
	if err != nil || group == nil || group.AntiSpamEnabled == 0 {
		return false
	}

	var customWords []string
	if group.ProfanityWords != nil && *group.ProfanityWords != "" {
		for _, w := range strings.Split(*group.ProfanityWords, ",") {
			if strings.TrimSpace(w) != "" {
				customWords = append(customWords, strings.TrimSpace(w))
			}
		}
	}

	if prompt.ContainsProfanity(text, customWords) {
		// Profanity detected
		userIDStr := fmt.Sprintf("%d", msg.From.ID)
		username := msg.From.Username
		if username == "" {
			username = msg.From.FirstName
		}

		if group.DeleteOnProfanity == 1 {
			_ = h.botClient.DeleteMessage(ctx, msg.Chat.ID, msg.MessageID)
		}

		reason := "Using prohibited abusive language"
		count, _ := h.warningRepo.AddWarning(ctx, chatIDStr, userIDStr, username, reason, "AutoMod")
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, userIDStr, username, "warn", reason, 0, "AutoMod")

		if count >= group.BanWarningLimit {
			_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, msg.From.ID, 0, true)
			_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, userIDStr)
			banNotice := fmt.Sprintf("🚨 <b>%s</b> has been <b>BANNED</b> for repeated abusive language (%d/%d warnings).",
				username, count, group.BanWarningLimit)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, banNotice, telegram.SendMessageOptions{ParseMode: "HTML"})
		} else {
			warnNotice := fmt.Sprintf("⚠️ <b>%s</b>, profanity is not allowed in this group! Warning <b>(%d/%d)</b>.",
				username, count, group.BanWarningLimit)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, warnNotice, telegram.SendMessageOptions{ParseMode: "HTML"})
		}
		return true
	}

	// Check unauthorized Telegram invite links
	if isTelegramInviteLink(text) {
		userIDStr := fmt.Sprintf("%d", msg.From.ID)
		username := msg.From.Username
		if username == "" {
			username = msg.From.FirstName
		}

		_ = h.botClient.DeleteMessage(ctx, msg.Chat.ID, msg.MessageID)
		reason := "Posting unauthorized Telegram invite links"
		count, _ := h.warningRepo.AddWarning(ctx, chatIDStr, userIDStr, username, reason, "AutoMod")
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, userIDStr, username, "warn", reason, 0, "AutoMod")

		if count >= group.BanWarningLimit {
			_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, msg.From.ID, 0, true)
			_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, userIDStr)
			banNotice := fmt.Sprintf("🚨 <b>%s</b> has been <b>BANNED</b> for unauthorized link spam (%d/%d warnings).",
				username, count, group.BanWarningLimit)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, banNotice, telegram.SendMessageOptions{ParseMode: "HTML"})
		} else {
			warnNotice := fmt.Sprintf("⚠️ <b>%s</b>, invite links are not allowed in this group! Warning <b>(%d/%d)</b>.",
				username, count, group.BanWarningLimit)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, warnNotice, telegram.SendMessageOptions{ParseMode: "HTML"})
		}
		return true
	}

	// Check mass mentions
	mentionLimit := group.MentionLimit
	if mentionLimit <= 0 {
		mentionLimit = 5
	}
	if countMentions(text) > mentionLimit {
		userIDStr := fmt.Sprintf("%d", msg.From.ID)
		username := msg.From.Username
		if username == "" {
			username = msg.From.FirstName
		}

		_ = h.botClient.DeleteMessage(ctx, msg.Chat.ID, msg.MessageID)
		reason := fmt.Sprintf("Mass mentioning (>%d users tagged)", mentionLimit)
		count, _ := h.warningRepo.AddWarning(ctx, chatIDStr, userIDStr, username, reason, "AutoMod")
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, userIDStr, username, "warn", reason, 0, "AutoMod")

		warnNotice := fmt.Sprintf("⚠️ <b>%s</b>, mass tagging is not allowed! Warning <b>(%d/%d)</b>.",
			username, count, group.BanWarningLimit)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, warnNotice, telegram.SendMessageOptions{ParseMode: "HTML"})
		return true
	}

	return false
}

func isTelegramInviteLink(text string) bool {
	lower := strings.ToLower(text)
	invitePatterns := []string{
		"t.me/joinchat/",
		"t.me/+",
		"telegram.me/joinchat/",
		"telegram.me/+",
		"telegram.dog/joinchat/",
		"telegram.dog/+",
	}
	for _, p := range invitePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func countMentions(text string) int {
	return strings.Count(text, "@")
}
