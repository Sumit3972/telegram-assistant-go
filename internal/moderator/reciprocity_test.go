package moderator

import (
	"testing"

	"telegram-ai-assistant/internal/domain"
)

func TestPhotoReciprocityLogic(t *testing.T) {
	h := &ConversationHandler{}

	userID := "12345678"

	// 1. Initially, no photo has been sent to user
	if h.hasUserPhotoPending(userID) {
		t.Fatalf("expected hasUserPhotoPending to be false initially")
	}

	// 2. Chavi sends a photo to user -> gate becomes active
	h.markPhotoSentToUser(userID)
	if !h.hasUserPhotoPending(userID) {
		t.Fatalf("expected hasUserPhotoPending to be true after marking photo sent")
	}

	// 3. User sends a photo back -> gate clears
	h.clearUserPhotoPending(userID)
	if h.hasUserPhotoPending(userID) {
		t.Fatalf("expected hasUserPhotoPending to be false after user sends photo")
	}
}

func TestGetBotAPIChatID(t *testing.T) {
	h := &ConversationHandler{}

	// Supergroup with positive ID from MTProto
	supergroupMsg := &domain.TelegramMessage{
		Chat: domain.TelegramChat{
			ID:   4443010452,
			Type: "supergroup",
		},
	}
	botChatID := h.getBotAPIChatID(supergroupMsg)
	if botChatID != -1004443010452 {
		t.Fatalf("expected -1004443010452, got %d", botChatID)
	}

	// Private chat with positive ID
	privateMsg := &domain.TelegramMessage{
		Chat: domain.TelegramChat{
			ID:   6009675121,
			Type: "private",
		},
	}
	botPrivateChatID := h.getBotAPIChatID(privateMsg)
	if botPrivateChatID != 6009675121 {
		t.Fatalf("expected 6009675121, got %d", botPrivateChatID)
	}
}
