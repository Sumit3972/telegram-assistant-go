package moderator

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"

	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/telegram"
)

type CaptchaHandler struct {
	captchaRepo domain.CaptchaRepository
	botClient   *telegram.BotClient
}

func NewCaptchaHandler(captchaRepo domain.CaptchaRepository, botClient *telegram.BotClient) *CaptchaHandler {
	return &CaptchaHandler{
		captchaRepo: captchaRepo,
		botClient:   botClient,
	}
}

func (h *CaptchaHandler) HandleNewMember(ctx context.Context, chatID int64, member domain.TelegramUser) {
	if member.IsBot {
		return
	}

	// 1. Restrict member until CAPTCHA is solved
	f := false
	_ = h.botClient.RestrictChatMember(ctx, chatID, member.ID, telegram.ChatPermissions{
		CanSendMessages:       &f,
		CanSendAudios:         &f,
		CanSendDocuments:      &f,
		CanSendPhotos:         &f,
		CanSendVideos:         &f,
		CanSendOtherMessages:  &f,
		CanAddWebPagePreviews: &f,
	}, 0)

	// 2. Generate math CAPTCHA
	num1 := rand.Intn(8) + 1
	num2 := rand.Intn(8) + 1
	correct := num1 + num2

	wrong1 := correct + rand.Intn(3) + 1
	wrong2 := correct - (rand.Intn(2) + 1)
	if wrong2 == correct || wrong2 <= 0 {
		wrong2 = correct + 4
	}

	options := []int{correct, wrong1, wrong2}
	rand.Shuffle(len(options), func(i, j int) { options[i], options[j] = options[j], options[i] })

	name := member.FirstName
	if name == "" {
		name = member.Username
	}

	var buttons []telegram.InlineKeyboardButton
	for _, opt := range options {
		buttons = append(buttons, telegram.InlineKeyboardButton{
			Text:         strconv.Itoa(opt),
			CallbackData: fmt.Sprintf("captcha:%d:%d", member.ID, opt),
		})
	}

	keyboard := telegram.InlineKeyboardMarkup{
		InlineKeyboard: [][]telegram.InlineKeyboardButton{buttons},
	}

	text := fmt.Sprintf("👋 Welcome, <b>%s</b>!\n\nPlease solve this CAPTCHA within 2 minutes to chat:\n\n<b>What is %d + %d = ?</b>",
		name, num1, num2)

	msg, err := h.botClient.SendMessage(ctx, chatID, text, telegram.SendMessageOptions{
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})
	if err != nil || msg == nil {
		return
	}

	// 3. Save session in DB
	cIDStr := fmt.Sprintf("%d", chatID)
	uIDStr := fmt.Sprintf("%d", member.ID)
	_ = h.captchaRepo.SetCaptchaSession(ctx, cIDStr, uIDStr, member.Username, strconv.Itoa(correct), strconv.Itoa(msg.MessageID))
}

func (h *CaptchaHandler) HandleCallbackQuery(ctx context.Context, cb *domain.CallbackQuery) bool {
	if cb == nil || cb.Message == nil || cb.Data == "" {
		return false
	}

	var targetUserID int64
	var selectedAns int
	n, _ := fmt.Sscanf(cb.Data, "captcha:%d:%d", &targetUserID, &selectedAns)
	if n != 2 {
		return false
	}

	// Only the user who joined can solve their own CAPTCHA
	if cb.From.ID != targetUserID {
		_ = h.botClient.AnswerCallbackQuery(ctx, cb.ID, "⚠️ This CAPTCHA is not for you!", true)
		return true
	}

	chatIDStr := fmt.Sprintf("%d", cb.Message.Chat.ID)
	userIDStr := fmt.Sprintf("%d", cb.From.ID)

	session, err := h.captchaRepo.GetCaptchaSession(ctx, chatIDStr, userIDStr)
	if err != nil || session == nil {
		_ = h.botClient.AnswerCallbackQuery(ctx, cb.ID, "CAPTCHA expired or already verified.", false)
		return true
	}

	if session.CorrectOption == strconv.Itoa(selectedAns) {
		// Correct! Unmute user
		t := true
		_ = h.botClient.RestrictChatMember(ctx, cb.Message.Chat.ID, cb.From.ID, telegram.ChatPermissions{
			CanSendMessages:       &t,
			CanSendAudios:         &t,
			CanSendDocuments:      &t,
			CanSendPhotos:         &t,
			CanSendVideos:         &t,
			CanSendOtherMessages:  &t,
			CanAddWebPagePreviews: &t,
		}, 0)

		_ = h.captchaRepo.DeleteCaptchaSession(ctx, chatIDStr, userIDStr)
		_ = h.botClient.DeleteMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID)
		_ = h.botClient.AnswerCallbackQuery(ctx, cb.ID, "✅ Verified! Welcome to the group.", false)

		welcomeMsg := fmt.Sprintf("🎉 Welcome <b>%s</b>! You are verified and can now send messages.", cb.From.FirstName)
		_, _ = h.botClient.SendMessage(ctx, cb.Message.Chat.ID, welcomeMsg, telegram.SendMessageOptions{
			ParseMode: "HTML",
		})
	} else {
		// Wrong answer
		_ = h.botClient.AnswerCallbackQuery(ctx, cb.ID, "❌ Incorrect answer. Please try again or wait for admin.", true)
	}

	return true
}
