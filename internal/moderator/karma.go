package moderator

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/telegram"
)

var karmaPlusRegex = regexp.MustCompile(`(?i)^\s*(\+|\+1|thx|thanks|thank you|ty|shukriya|dhanyawad|dhanyavad|nice|great|good|awesome|helpful|respect|\+rep|good bot)\b`)
var karmaMinusRegex = regexp.MustCompile(`(?i)^\s*(\-|\-1|bad|useless|unhelpful|scam|spam|\-rep|bad bot)\b`)

type KarmaHandler struct {
	karmaRepo domain.KarmaRepository
	botClient *telegram.BotClient
}

func NewKarmaHandler(karmaRepo domain.KarmaRepository, botClient *telegram.BotClient) *KarmaHandler {
	return &KarmaHandler{
		karmaRepo: karmaRepo,
		botClient: botClient,
	}
}

func (h *KarmaHandler) HandleKarma(ctx context.Context, msg *domain.TelegramMessage) {
	if msg == nil || msg.ReplyToMessage == nil || msg.ReplyToMessage.From == nil {
		return
	}

	sender := msg.From
	target := msg.ReplyToMessage.From
	if sender == nil || target == nil || target.IsBot || sender.ID == target.ID {
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}
	if text == "" {
		return
	}

	delta := 0
	if karmaPlusRegex.MatchString(text) {
		delta = 1
	} else if karmaMinusRegex.MatchString(text) {
		delta = -1
	}

	if delta == 0 {
		return
	}

	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	targetUserID := fmt.Sprintf("%d", target.ID)
	targetUsername := target.Username
	if targetUsername == "" {
		targetUsername = target.FirstName
	}

	newPoints, err := h.karmaRepo.AddKarma(ctx, chatID, targetUserID, targetUsername, delta)
	if err != nil {
		return
	}

	actionStr := "increased"
	emoji := "🌟"
	if delta < 0 {
		actionStr = "decreased"
		emoji = "🔻"
	}

	replyMsg := fmt.Sprintf("%s Karma %s for <b>%s</b>! Total karma: <b>%d</b> points.",
		emoji, actionStr, targetUsername, newPoints)

	_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, replyMsg, telegram.SendMessageOptions{
		ParseMode: "HTML",
		ReplyToID: msg.MessageID,
	})
}
