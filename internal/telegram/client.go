package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"telegram-ai-assistant/internal/domain"
)

type BotClient struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func NewBotClient(token string) *BotClient {
	return &BotClient{
		token:      token,
		baseURL:    fmt.Sprintf("https://api.telegram.org/bot%s", token),
		httpClient: &http.Client{Timeout: 45 * time.Second},
	}
}

type telegramAPIResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description,omitempty"`
	ErrorCode   int    `json:"error_code,omitempty"`
}

func (c *BotClient) Request(ctx context.Context, method string, reqBody any, resultTarget any) error {
	url := fmt.Sprintf("%s/%s", c.baseURL, method)

	var reqReader io.Reader
	if reqBody != nil {
		jsonBytes, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqReader = bytes.NewReader(jsonBytes)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reqReader)
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}

	if reqBody != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("telegram request %s failed: %w", method, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read telegram response: %w", err)
	}

	var apiResp struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description,omitempty"`
		ErrorCode   int             `json:"error_code,omitempty"`
	}

	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return fmt.Errorf("failed to unmarshal telegram response: %w (body: %s)", err, string(respBytes))
	}

	if !apiResp.OK {
		return fmt.Errorf("telegram API error [%d]: %s", apiResp.ErrorCode, apiResp.Description)
	}

	if resultTarget != nil && len(apiResp.Result) > 0 {
		if err := json.Unmarshal(apiResp.Result, resultTarget); err != nil {
			return fmt.Errorf("failed to unmarshal result: %w", err)
		}
	}

	return nil
}

func (c *BotClient) SendMessage(ctx context.Context, chatID any, text string, opts SendMessageOptions) (*domain.TelegramMessage, error) {
	processedText, parseMode := ProcessTextAndParseMode(text, opts.ParseMode)

	body := map[string]any{
		"chat_id": fmt.Sprintf("%v", chatID),
		"text":    processedText,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	if opts.ReplyToID > 0 {
		body["reply_to_message_id"] = opts.ReplyToID
	}
	if opts.ReplyMarkup != nil {
		body["reply_markup"] = opts.ReplyMarkup
	}
	if opts.ProtectContent {
		body["protect_content"] = true
	}

	var msg domain.TelegramMessage
	err := c.Request(ctx, "sendMessage", body, &msg)
	if err != nil {
		// If HTML parse error, fallback to plain text
		if parseMode != "" {
			delete(body, "parse_mode")
			body["text"] = text
			var fallbackMsg domain.TelegramMessage
			if fErr := c.Request(ctx, "sendMessage", body, &fallbackMsg); fErr == nil {
				return &fallbackMsg, nil
			}
		}
		return nil, err
	}
	return &msg, nil
}

func (c *BotClient) SendPhoto(ctx context.Context, chatID any, photoData []byte, photoURL, caption string, replyToID int, parseMode string) (*domain.TelegramMessage, error) {
	processedCaption, pMode := ProcessTextAndParseMode(caption, parseMode)

	if len(photoData) > 0 {
		url := fmt.Sprintf("%s/sendPhoto", c.baseURL)
		var b bytes.Buffer
		w := multipart.NewWriter(&b)

		_ = w.WriteField("chat_id", fmt.Sprintf("%v", chatID))
		if processedCaption != "" {
			_ = w.WriteField("caption", processedCaption)
		}
		if pMode != "" {
			_ = w.WriteField("parse_mode", pMode)
		}
		if replyToID > 0 {
			_ = w.WriteField("reply_to_message_id", strconv.Itoa(replyToID))
		}

		part, err := w.CreateFormFile("photo", "photo.jpg")
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(photoData); err != nil {
			return nil, err
		}
		_ = w.Close()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &b)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", w.FormDataContentType())

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		var apiResp telegramAPIResponse[domain.TelegramMessage]
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return nil, err
		}
		if !apiResp.OK {
			return nil, fmt.Errorf("sendPhoto failed: %s", apiResp.Description)
		}
		return &apiResp.Result, nil
	}

	body := map[string]any{
		"chat_id": fmt.Sprintf("%v", chatID),
		"photo":   photoURL,
	}
	if processedCaption != "" {
		body["caption"] = processedCaption
	}
	if pMode != "" {
		body["parse_mode"] = pMode
	}
	if replyToID > 0 {
		body["reply_to_message_id"] = replyToID
	}

	var msg domain.TelegramMessage
	if err := c.Request(ctx, "sendPhoto", body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (c *BotClient) SendVoice(ctx context.Context, chatID any, voiceData []byte, replyToID int) (*domain.TelegramMessage, error) {
	url := fmt.Sprintf("%s/sendVoice", c.baseURL)
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	_ = w.WriteField("chat_id", fmt.Sprintf("%v", chatID))
	if replyToID > 0 {
		_ = w.WriteField("reply_to_message_id", strconv.Itoa(replyToID))
	}

	part, err := w.CreateFormFile("voice", "voice.mp3")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(voiceData); err != nil {
		return nil, err
	}
	_ = w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &b)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp telegramAPIResponse[domain.TelegramMessage]
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, err
	}
	if !apiResp.OK {
		return nil, fmt.Errorf("sendVoice failed: %s", apiResp.Description)
	}
	return &apiResp.Result, nil
}

func (c *BotClient) SendSticker(ctx context.Context, chatID any, sticker string, replyToID int) (*domain.TelegramMessage, error) {
	body := map[string]any{
		"chat_id": fmt.Sprintf("%v", chatID),
		"sticker": sticker,
	}
	if replyToID > 0 {
		body["reply_to_message_id"] = replyToID
	}
	var msg domain.TelegramMessage
	if err := c.Request(ctx, "sendSticker", body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (c *BotClient) SendChatAction(ctx context.Context, chatID any, action string) error {
	body := map[string]any{
		"chat_id": fmt.Sprintf("%v", chatID),
		"action":  action,
	}
	var res bool
	return c.Request(ctx, "sendChatAction", body, &res)
}

func (c *BotClient) SetMessageReaction(ctx context.Context, chatID any, messageID int, emoji string) bool {
	body := map[string]any{
		"chat_id":    fmt.Sprintf("%v", chatID),
		"message_id": messageID,
		"reaction": []ReactionTypeEmoji{
			{Type: "emoji", Emoji: emoji},
		},
	}

	var res bool
	err := c.Request(ctx, "setMessageReaction", body, &res)
	if err != nil {
		log.Printf("[BotClient] Reaction %s failed: %v. Trying fallback 👍...", emoji, err)
		if emoji != "👍" {
			body["reaction"] = []ReactionTypeEmoji{
				{Type: "emoji", Emoji: "👍"},
			}
			_ = c.Request(ctx, "setMessageReaction", body, &res)
		}
		return false
	}
	return true
}

func (c *BotClient) RestrictChatMember(ctx context.Context, chatID any, userID any, perms ChatPermissions, untilDate int64) error {
	uID, _ := strconv.ParseInt(fmt.Sprintf("%v", userID), 10, 64)
	body := map[string]any{
		"chat_id":     fmt.Sprintf("%v", chatID),
		"user_id":     uID,
		"permissions": perms,
	}
	if untilDate > 0 {
		body["until_date"] = untilDate
	}
	var res bool
	return c.Request(ctx, "restrictChatMember", body, &res)
}

func (c *BotClient) BanChatMember(ctx context.Context, chatID any, userID any, untilDate int64, revokeMessages bool) error {
	uID, _ := strconv.ParseInt(fmt.Sprintf("%v", userID), 10, 64)
	body := map[string]any{
		"chat_id":         fmt.Sprintf("%v", chatID),
		"user_id":         uID,
		"revoke_messages": revokeMessages,
	}
	if untilDate > 0 {
		body["until_date"] = untilDate
	}
	var res bool
	return c.Request(ctx, "banChatMember", body, &res)
}

func (c *BotClient) UnbanChatMember(ctx context.Context, chatID any, userID any, onlyIfBanned bool) error {
	uID, _ := strconv.ParseInt(fmt.Sprintf("%v", userID), 10, 64)
	body := map[string]any{
		"chat_id":        fmt.Sprintf("%v", chatID),
		"user_id":        uID,
		"only_if_banned": onlyIfBanned,
	}
	var res bool
	return c.Request(ctx, "unbanChatMember", body, &res)
}

func (c *BotClient) GetChatMember(ctx context.Context, chatID any, userID any) (*ChatMember, error) {
	uID, _ := strconv.ParseInt(fmt.Sprintf("%v", userID), 10, 64)
	body := map[string]any{
		"chat_id": fmt.Sprintf("%v", chatID),
		"user_id": uID,
	}
	var member ChatMember
	if err := c.Request(ctx, "getChatMember", body, &member); err != nil {
		return nil, err
	}
	return &member, nil
}

func (c *BotClient) GetChatAdministrators(ctx context.Context, chatID any) ([]ChatMember, error) {
	body := map[string]any{
		"chat_id": fmt.Sprintf("%v", chatID),
	}
	var admins []ChatMember
	if err := c.Request(ctx, "getChatAdministrators", body, &admins); err != nil {
		return nil, err
	}
	return admins, nil
}

func (c *BotClient) PinChatMessage(ctx context.Context, chatID any, messageID int, disableNotification bool) error {
	body := map[string]any{
		"chat_id":              fmt.Sprintf("%v", chatID),
		"message_id":           messageID,
		"disable_notification": disableNotification,
	}
	var res bool
	return c.Request(ctx, "pinChatMessage", body, &res)
}

func (c *BotClient) DeleteMessage(ctx context.Context, chatID any, messageID int) error {
	body := map[string]any{
		"chat_id":    fmt.Sprintf("%v", chatID),
		"message_id": messageID,
	}
	var res bool
	return c.Request(ctx, "deleteMessage", body, &res)
}

func (c *BotClient) AnswerCallbackQuery(ctx context.Context, callbackQueryID, text string, showAlert bool) error {
	body := map[string]any{
		"callback_query_id": callbackQueryID,
		"text":              text,
		"show_alert":        showAlert,
	}
	var res bool
	return c.Request(ctx, "answerCallbackQuery", body, &res)
}

func (c *BotClient) GetMe(ctx context.Context) (*domain.TelegramUser, error) {
	var user domain.TelegramUser
	if err := c.Request(ctx, "getMe", nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (c *BotClient) GetFile(ctx context.Context, fileID string) (*domain.TelegramFile, error) {
	body := map[string]any{
		"file_id": fileID,
	}
	var file domain.TelegramFile
	if err := c.Request(ctx, "getFile", body, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

func (c *BotClient) SetWebhook(ctx context.Context, webhookURL string, allowedUpdates []string) (bool, error) {
	body := map[string]any{
		"url":             webhookURL,
		"allowed_updates": allowedUpdates,
	}
	var res bool
	if err := c.Request(ctx, "setWebhook", body, &res); err != nil {
		return false, err
	}
	return res, nil
}
