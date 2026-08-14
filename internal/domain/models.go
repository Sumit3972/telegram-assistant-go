package domain

import (
	"encoding/json"
	"time"
)

// --- Telegram Models ---

type TelegramUser struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

type TelegramChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"` // "private", "group", "supergroup", "channel"
	Title     string `json:"title,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size,omitempty"`
}

type TelegramMessage struct {
	MessageID       int              `json:"message_id"`
	From            *TelegramUser    `json:"from,omitempty"`
	SenderChat      *TelegramChat    `json:"sender_chat,omitempty"`
	Date            int64            `json:"date"`
	Chat            TelegramChat     `json:"chat"`
	ForwardFrom     *TelegramUser    `json:"forward_from,omitempty"`
	ForwardDate     int64            `json:"forward_date,omitempty"`
	ReplyToMessage  *TelegramMessage `json:"reply_to_message,omitempty"`
	Text            string           `json:"text,omitempty"`
	Caption         string           `json:"caption,omitempty"`
	Entities        []any            `json:"entities,omitempty"`
	Photo           []PhotoSize      `json:"photo,omitempty"`
	NewChatMembers  []TelegramUser   `json:"new_chat_members,omitempty"`
	LeftChatMember  *TelegramUser    `json:"left_chat_member,omitempty"`
	NewChatTitle    string           `json:"new_chat_title,omitempty"`
	MigrateToChatID int64            `json:"migrate_to_chat_id,omitempty"`
}

type CallbackQuery struct {
	ID              string           `json:"id"`
	From            TelegramUser     `json:"from"`
	Message         *TelegramMessage `json:"message,omitempty"`
	InlineMessageID string           `json:"inline_message_id,omitempty"`
	ChatInstance    string           `json:"chat_instance"`
	Data            string           `json:"data,omitempty"`
}

type TelegramUpdate struct {
	UpdateID      int              `json:"update_id"`
	Message       *TelegramMessage `json:"message,omitempty"`
	EditedMessage *TelegramMessage `json:"edited_message,omitempty"`
	CallbackQuery *CallbackQuery   `json:"callback_query,omitempty"`
}

type TelegramFile struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int    `json:"file_size,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
}

// --- Database Domain Models ---

type GroupSettings struct {
	ChatID             string    `json:"chat_id"`
	Title              string    `json:"title"`
	Rules              string    `json:"rules"`
	AntiSpamEnabled    int       `json:"anti_spam_enabled"`
	WelcomeMessage     string    `json:"welcome_message"`
	CaptchaEnabled     int       `json:"captcha_enabled"`
	ProfanityWords     *string   `json:"profanity_words"`
	BanWarningLimit    int       `json:"ban_warning_limit"`
	MentionLimit       int       `json:"mention_limit"`
	DeleteOnProfanity  int       `json:"delete_on_profanity"`
	ReportOnProfanity  int       `json:"report_on_profanity"`
	CreatedAt          int64     `json:"created_at"`
}

type AdminInfo struct {
	ChatID        string `json:"chat_id"`
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	PrivateChatID string `json:"private_chat_id"`
}

type WarningInfo struct {
	ID        int64  `json:"id"`
	ChatID    string `json:"chat_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Reason    string `json:"reason"`
	WarnedBy  string `json:"warned_by"`
	CreatedAt int64  `json:"created_at"`
}

type ModerationLog struct {
	ID              int64  `json:"id"`
	ChatID          string `json:"chat_id"`
	UserID          string `json:"user_id"`
	Username        string `json:"username"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	DurationMinutes int    `json:"duration_minutes"`
	ExecutedBy      string `json:"executed_by"`
	CreatedAt       int64  `json:"created_at"`
}

type ChatMessageLog struct {
	ID               int64  `json:"id"`
	ChatID           string `json:"chat_id"`
	UserID           string `json:"user_id"`
	Username         string `json:"username"`
	MessageText      string `json:"message_text"`
	MessageID        string `json:"message_id"`
	ReplyToMessageID string `json:"reply_to_message_id"`
	CreatedAt        int64  `json:"created_at"`
}

type KarmaInfo struct {
	ChatID   string `json:"chat_id"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Points   int    `json:"points"`
}

type CaptchaSession struct {
	ChatID        string `json:"chat_id"`
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	CorrectOption string `json:"correct_option"`
	MessageID     string `json:"message_id"`
	CreatedAt     int64  `json:"created_at"`
}

type MentionSession struct {
	ChatID       string `json:"chat_id"`
	MessageID    string `json:"message_id"`
	AdminUserID  string `json:"admin_user_id"`
	SenderUserID string `json:"sender_user_id"`
	CreatedAt    int64  `json:"created_at"`
}

type Relationship struct {
	ChatID          string `json:"chat_id"`
	UserID          string `json:"user_id"`
	Username        string `json:"username"`
	AffectionScore  int    `json:"affection_score"`
	LastInteraction int64  `json:"last_interaction"`
}

type DailyShip struct {
	ChatID    string `json:"chat_id"`
	User1ID   string `json:"user1_id"`
	User1Name string `json:"user1_name"`
	User2ID   string `json:"user2_id"`
	User2Name string `json:"user2_name"`
	ShipDate  string `json:"ship_date"`
}

type ApiKeyRecord struct {
	ID          int        `json:"id"`
	Provider    string     `json:"provider"`
	APIKey      string     `json:"api_key"`
	Status      string     `json:"status"` // "active", "exhausted"
	ExhaustedAt *time.Time `json:"exhausted_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type ModelPerformance struct {
	ProviderURL        string `json:"provider_url"`
	ModelName          string `json:"model_name"`
	TotalRequests      int    `json:"total_requests"`
	SuccessfulRequests int    `json:"successful_requests"`
	TotalLatencyMs     int64  `json:"total_latency_ms"`
	AvgLatencyMs       int    `json:"avg_latency_ms"`
}

// --- AI Models ---

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type ChatMessageContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *ImageURLDetail `json:"image_url,omitempty"`
}

type ImageURLDetail struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type ChatMessage struct {
	Role       string     `json:"role"` // "system", "user", "assistant", "tool"
	Content    any        `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

func (m ChatMessage) GetStringContent() string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case []ChatMessageContentPart:
		for _, part := range v {
			if part.Type == "text" {
				return part.Text
			}
		}
	}
	return ""
}

type ToolFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

type VoiceResponseData struct {
	ShouldSpeak  bool    `json:"should_speak"`
	LanguageCode string  `json:"language_code,omitempty"`
	TTSText      string  `json:"tts_text,omitempty"`
	Pace         float64 `json:"pace,omitempty"`
	Temperature  float64 `json:"temperature,omitempty"`
}

type StructuredAIResponse struct {
	ShouldReply     bool               `json:"should_reply"`
	DynamicEmoji    *string            `json:"dynamic_emoji"`
	ReplyText       string             `json:"reply_text"`
	AffectionChange int                `json:"affection_change"`
	SelfiePrompt    *string            `json:"selfie_prompt"`
	StickerEmoji    *string            `json:"sticker_emoji"`
	VoiceResponse   *VoiceResponseData `json:"voice_response"`
}

func UnmarshalStructuredAIResponse(data []byte) (*StructuredAIResponse, error) {
	var resp StructuredAIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
