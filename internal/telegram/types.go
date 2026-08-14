package telegram

import (
	"telegram-ai-assistant/internal/domain"
)

type SendMessageOptions struct {
	ParseMode      string `json:"parse_mode,omitempty"`
	ReplyToID      int    `json:"reply_to_message_id,omitempty"`
	ReplyMarkup    any    `json:"reply_markup,omitempty"`
	ProtectContent bool   `json:"protect_content,omitempty"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type ChatPermissions struct {
	CanSendMessages       *bool `json:"can_send_messages,omitempty"`
	CanSendAudios         *bool `json:"can_send_audios,omitempty"`
	CanSendDocuments      *bool `json:"can_send_documents,omitempty"`
	CanSendPhotos         *bool `json:"can_send_photos,omitempty"`
	CanSendVideos         *bool `json:"can_send_videos,omitempty"`
	CanSendVideoNotes     *bool `json:"can_send_video_notes,omitempty"`
	CanSendVoiceNotes     *bool `json:"can_send_voice_notes,omitempty"`
	CanSendPolls          *bool `json:"can_send_polls,omitempty"`
	CanSendOtherMessages  *bool `json:"can_send_other_messages,omitempty"`
	CanAddWebPagePreviews *bool `json:"can_add_web_page_previews,omitempty"`
	CanChangeInfo         *bool `json:"can_change_info,omitempty"`
	CanInviteUsers        *bool `json:"can_invite_users,omitempty"`
	CanPinMessages        *bool `json:"can_pin_messages,omitempty"`
	CanManageTopics       *bool `json:"can_manage_topics,omitempty"`
}

type ChatMember struct {
	Status      string              `json:"status"` // "creator", "administrator", "member", "restricted", "left", "kicked"
	User        domain.TelegramUser `json:"user"`
	CanBeEdited bool                `json:"can_be_edited,omitempty"`
}

type ReactionTypeEmoji struct {
	Type  string `json:"type"` // "emoji"
	Emoji string `json:"emoji"`
}
