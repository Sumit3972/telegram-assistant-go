package moderator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"telegram-ai-assistant/internal/ai"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/media"
	"telegram-ai-assistant/internal/prompt"
	"telegram-ai-assistant/internal/telegram"
)

type ConversationHandler struct {
	aiClient      *ai.Client
	imageService  *media.ImageService
	voiceService  *media.VoiceService
	searchService *media.SearchService
	musicService  *media.MusicService
	historyRepo   domain.HistoryRepository
	relRepo       domain.RelationshipRepository
	groupRepo     domain.GroupRepository
	adminRepo     domain.AdminRepository
	warningRepo   domain.WarningRepository
	modLogRepo    domain.ModerationLogRepository
	botClient     *telegram.BotClient
	userbotSender domain.UserbotSender
	botName       string
	botUsername   string
	fallbackKey   string
}

func (h *ConversationHandler) SetUserbotSender(s domain.UserbotSender) {
	h.userbotSender = s
}

func NewConversationHandler(
	aiClient *ai.Client,
	imageService *media.ImageService,
	voiceService *media.VoiceService,
	searchService *media.SearchService,
	musicService *media.MusicService,
	historyRepo domain.HistoryRepository,
	relRepo domain.RelationshipRepository,
	groupRepo domain.GroupRepository,
	adminRepo domain.AdminRepository,
	warningRepo domain.WarningRepository,
	modLogRepo domain.ModerationLogRepository,
	botClient *telegram.BotClient,
	botName, botUsername, fallbackKey string,
) *ConversationHandler {
	return &ConversationHandler{
		aiClient:      aiClient,
		imageService:  imageService,
		voiceService:  voiceService,
		searchService: searchService,
		musicService:  musicService,
		historyRepo:   historyRepo,
		relRepo:       relRepo,
		groupRepo:     groupRepo,
		adminRepo:     adminRepo,
		warningRepo:   warningRepo,
		modLogRepo:    modLogRepo,
		botClient:     botClient,
		botName:       botName,
		botUsername:   botUsername,
		fallbackKey:   fallbackKey,
	}
}

func (h *ConversationHandler) sendChatAction(ctx context.Context, chatID int64, isUserbot bool, action string) {
	if isUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		_ = h.userbotSender.SendChatAction(ctx, chatID, action)
		return
	}
	if h.botClient != nil {
		_ = h.botClient.SendChatAction(ctx, chatID, action)
	}
}

func (h *ConversationHandler) HandleConversation(ctx context.Context, msg *domain.TelegramMessage, isAdmin bool) {
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIDStr := fmt.Sprintf("%d", msg.From.ID)
	username := msg.From.Username
	if username == "" {
		username = msg.From.FirstName
	}

	// Trigger typing indicator immediately
	h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "typing")

	// 1. Get affection score
	affectionScore := 50
	if rel, err := h.relRepo.GetRelationship(ctx, chatIDStr, userIDStr); err == nil && rel != nil {
		affectionScore = rel.AffectionScore
	}

	// 2. Get group rules
	rules := ""
	if g, err := h.groupRepo.GetGroup(ctx, chatIDStr); err == nil && g != nil {
		rules = g.Rules
	}

	// 3. Get recent history (used directly for multi-turn ChatMessage array)
	recent, _ := h.historyRepo.GetRecentMessages(ctx, chatIDStr, 20)

	// 4. Build System Prompt (history is passed as ChatMessage turns to prevent duplicate token consumption)
	sysPrompt := prompt.BuildDynamicSystemPrompt(prompt.SystemPromptParams{
		Identity: prompt.IdentityParams{
			Name:     h.botName,
			Username: h.botUsername,
			Gender:   "female",
		},
		IsAdmin:        isAdmin,
		Username:       username,
		FirstName:      msg.From.FirstName,
		AffectionScore: affectionScore,
		Rules:          rules,
		UserText:       text,
		WithHistory:    false,
		HistoryContext: "",
	})

	// 5. Extract image/photo if present in current message or replied message for Vision AI
	var imageBase64 string
	if msg.ImageBase64 != "" {
		imageBase64 = msg.ImageBase64
	} else if len(msg.Photo) > 0 && h.botClient != nil {
		largest := msg.Photo[len(msg.Photo)-1]
		if imgURI, err := h.botClient.DownloadFileBase64(ctx, largest.FileID); err == nil && imgURI != "" {
			imageBase64 = imgURI
		}
	} else if msg.Animation != nil && msg.Animation.Thumb != nil && h.botClient != nil {
		if imgURI, err := h.botClient.DownloadFileBase64(ctx, msg.Animation.Thumb.FileID); err == nil && imgURI != "" {
			imageBase64 = imgURI
		}
	} else if msg.Sticker != nil && msg.Sticker.Thumb != nil && h.botClient != nil {
		if imgURI, err := h.botClient.DownloadFileBase64(ctx, msg.Sticker.Thumb.FileID); err == nil && imgURI != "" {
			imageBase64 = imgURI
		}
	}

	// Check replied message for image / GIF / sticker if not in current message
	if imageBase64 == "" && msg.ReplyToMessage != nil {
		if msg.ReplyToMessage.ImageBase64 != "" {
			imageBase64 = msg.ReplyToMessage.ImageBase64
		} else if len(msg.ReplyToMessage.Photo) > 0 && h.botClient != nil {
			largest := msg.ReplyToMessage.Photo[len(msg.ReplyToMessage.Photo)-1]
			if imgURI, err := h.botClient.DownloadFileBase64(ctx, largest.FileID); err == nil && imgURI != "" {
				imageBase64 = imgURI
			}
		} else if msg.ReplyToMessage.Animation != nil && msg.ReplyToMessage.Animation.Thumb != nil && h.botClient != nil {
			if imgURI, err := h.botClient.DownloadFileBase64(ctx, msg.ReplyToMessage.Animation.Thumb.FileID); err == nil && imgURI != "" {
				imageBase64 = imgURI
			}
		} else if msg.ReplyToMessage.Sticker != nil && msg.ReplyToMessage.Sticker.Thumb != nil && h.botClient != nil {
			if imgURI, err := h.botClient.DownloadFileBase64(ctx, msg.ReplyToMessage.Sticker.Thumb.FileID); err == nil && imgURI != "" {
				imageBase64 = imgURI
			}
		}
	}

	// 6. Assemble Tools
	var tools []domain.ToolDefinition
	tools = append(tools, ai.GetConversationTools()...)
	tools = append(tools, ai.GetContactAdminTools()...)
	if isAdmin {
		tools = append(tools, ai.GetAdminTools()...)
	}

	messages := []domain.ChatMessage{
		{
			Role:    "system",
			Content: sysPrompt,
		},
	}

	// Add multi-turn message history for rich conversational context
	isPrivateChat := msg.Chat.Type == "private"
	currentMsgIDStr := strconv.Itoa(msg.MessageID)
	hasCurrentInHistory := false

	for _, m := range recent {
		if m.MessageID != "" && m.MessageID == currentMsgIDStr {
			hasCurrentInHistory = true
		}
		isAssistant := m.UserID == "assistant" ||
			strings.EqualFold(m.Username, h.botUsername) ||
			strings.EqualFold(m.Username, h.botName)

		if isAssistant {
			messages = append(messages, domain.ChatMessage{
				Role:    "assistant",
				Content: m.MessageText,
			})
		} else {
			content := m.MessageText
			if !isPrivateChat && m.Username != "" {
				content = fmt.Sprintf("%s: %s", m.Username, m.MessageText)
			}
			messages = append(messages, domain.ChatMessage{
				Role:    "user",
				Content: content,
			})
		}
	}

	// If current message was not in recent history, append it as the latest user turn
	if !hasCurrentInHistory {
		content := text
		if content == "" && imageBase64 != "" {
			content = "Look at this image / GIF"
		}
		if !isPrivateChat && username != "" {
			content = fmt.Sprintf("%s: %s", username, content)
		}
		if imageBase64 != "" {
			log.Printf("[Conversation] Attaching multimodal image to AI request (base64 length=%d)", len(imageBase64))
			messages = append(messages, domain.ChatMessage{
				Role: "user",
				Content: []domain.ChatMessageContentPart{
					{
						Type: "text",
						Text: content,
					},
					{
						Type: "image_url",
						ImageURL: &domain.ImageURLDetail{
							URL: imageBase64,
						},
					},
				},
			})
		} else {
			messages = append(messages, domain.ChatMessage{
				Role:    "user",
				Content: content,
			})
		}
	} else if imageBase64 != "" {
		// Update last user message in multi-turn history to include vision image
		if len(messages) > 0 && messages[len(messages)-1].Role == "user" {
			lastMsg := messages[len(messages)-1]
			textStr := lastMsg.GetStringContent()
			log.Printf("[Conversation] Attaching multimodal image to last history user message (base64 length=%d)", len(imageBase64))
			messages[len(messages)-1] = domain.ChatMessage{
				Role: "user",
				Content: []domain.ChatMessageContentPart{
					{
						Type: "text",
						Text: textStr,
					},
					{
						Type: "image_url",
						ImageURL: &domain.ImageURLDetail{
							URL: imageBase64,
						},
					},
				},
			}
		}
	}

	opts := ai.ChatCompletionOptions{
		Tools: tools,
	}

	log.Printf("[Conversation] Calling AI for user @%s in chat %s (turns=%d)...", username, chatIDStr, len(messages))
	res, err := h.aiClient.ChatCompletions(ctx, messages, opts)
	if err != nil || res == nil {
		log.Printf("[Conversation] AI ChatCompletions error: %v", err)
		return
	}

	// 6. Handle Tool Calls if returned
	if len(res.Message.ToolCalls) > 0 {
		for _, tc := range res.Message.ToolCalls {
			h.executeToolCall(ctx, msg, tc, isAdmin, messages, userIDStr, username, chatIDStr)
		}
		return
	}

	// 7. Parse structured response
	content := res.Message.GetStringContent()
	h.parseAndDispatchResponse(ctx, msg, content, text, userIDStr, username, chatIDStr)
}

func (h *ConversationHandler) executeToolCall(
	ctx context.Context,
	msg *domain.TelegramMessage,
	tc domain.ToolCall,
	isAdmin bool,
	history []domain.ChatMessage,
	userIDStr, username, chatIDStr string,
) {
	name := tc.Function.Name
	argsJSON := tc.Function.Arguments
	log.Printf("[ToolCall] Executing tool %s with args: %s", name, argsJSON)

	var args map[string]any
	_ = json.Unmarshal([]byte(argsJSON), &args)

	switch name {
	case "web_search":
		query, _ := args["query"].(string)
		if query != "" {
			h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "typing")
			searchRes, err := h.searchService.Search(ctx, query)
			if err != nil {
				searchRes = fmt.Sprintf("Search error: %v", err)
			}

			// Add assistant message with tool calls AND tool response to satisfy OpenAI API standard
			toolMessages := append(history,
				domain.ChatMessage{
					Role:      "assistant",
					ToolCalls: []domain.ToolCall{tc},
				},
				domain.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       name,
					Content:    searchRes,
				},
			)

			finalRes, err := h.aiClient.ChatCompletions(ctx, toolMessages, ai.ChatCompletionOptions{})
			if err == nil && finalRes != nil {
				reply := finalRes.Message.GetStringContent()
				h.parseAndDispatchResponse(ctx, msg, reply, query, userIDStr, username, chatIDStr)
			}
		}

	case "send_photo":
		selfiePrompt, _ := args["selfie_prompt"].(string)
		replyText, _ := args["reply_text"].(string)
		go h.generateAndSendPhoto(context.Background(), msg, selfiePrompt, replyText)

	case "play_music", "skip_music", "pause_music", "resume_music", "stop_music":
		songName, _ := args["song_name"].(string)
		musicRes, _ := h.musicService.ExecuteMusicCommand(ctx, chatIDStr, name, songName)
		h.sendMessage(ctx, msg, musicRes)

	case "forward_to_admin":
		msgText, _ := args["message_text"].(string)
		adminUname, _ := args["admin_username"].(string)
		h.forwardToAdmin(ctx, msg, msgText, adminUname)

	case "mute_user", "ban_user", "unmute_user", "unban_user", "warn_user", "clear_warnings", "purge_messages":
		if isAdmin {
			h.executeAdminTool(ctx, msg, name, args)
		}
	}
}

func (h *ConversationHandler) generateAndSendPhoto(ctx context.Context, msg *domain.TelegramMessage, rawPrompt, replyText string) {
	cleanPrompt := strings.TrimSpace(rawPrompt)
	if cleanPrompt == "" || strings.EqualFold(cleanPrompt, "null") || strings.EqualFold(cleanPrompt, "none") {
		if replyText != "" {
			h.sendMessage(ctx, msg, replyText)
		}
		return
	}

	h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "upload_photo")
	log.Printf("[Conversation] Generating image directly with prompt: %s", cleanPrompt)
	img, err := h.imageService.GenerateImage(ctx, cleanPrompt)
	if err != nil || img == nil {
		log.Printf("[Conversation] Failed to generate image: %v", err)
		if replyText != "" {
			h.sendMessage(ctx, msg, replyText)
		}
		return
	}

	if len(img.Data) > 0 {
		h.sendPhoto(ctx, msg, img.Data, "", replyText)
	} else if img.URL != "" {
		h.sendPhoto(ctx, msg, nil, img.URL, replyText)
	}
}

func (h *ConversationHandler) parseAndDispatchResponse(
	ctx context.Context,
	msg *domain.TelegramMessage,
	aiContent, userText, userIDStr, username, chatIDStr string,
) {
	// Try parsing JSON structure
	cleanJSON := strings.TrimSpace(aiContent)
	if idx := strings.Index(cleanJSON, "{"); idx != -1 {
		if lastIdx := strings.LastIndex(cleanJSON, "}"); lastIdx != -1 && lastIdx > idx {
			cleanJSON = cleanJSON[idx : lastIdx+1]
		}
	}

	var resp domain.StructuredAIResponse
	err := json.Unmarshal([]byte(cleanJSON), &resp)
	if err != nil {
		// Fallback: if json parsing failed, treat cleaned raw string as reply text
		raw := strings.TrimSpace(aiContent)
		if idx := strings.Index(raw, "{"); idx != -1 {
			pre := strings.TrimSpace(raw[:idx])
			if pre != "" {
				raw = pre
			}
		}
		resp = domain.StructuredAIResponse{
			ShouldReply: true,
			ReplyText:   raw,
		}
	}

	// Update affection
	if resp.AffectionChange != 0 {
		_, _ = h.relRepo.UpdateAffection(ctx, chatIDStr, userIDStr, username, resp.AffectionChange)
	}

	if !resp.ShouldReply {
		if resp.DynamicEmoji != nil && *resp.DynamicEmoji != "" {
			h.setReaction(ctx, msg, *resp.DynamicEmoji)
		}
		return
	}

	// Handle selfie request if present
	if resp.SelfiePrompt != nil && *resp.SelfiePrompt != "" && !strings.EqualFold(*resp.SelfiePrompt, "null") && !strings.EqualFold(*resp.SelfiePrompt, "none") {
		go h.generateAndSendPhoto(context.Background(), msg, *resp.SelfiePrompt, resp.ReplyText)
		return
	}

	// Handle Voice generation if requested
	if resp.VoiceResponse != nil && resp.VoiceResponse.ShouldSpeak && resp.VoiceResponse.TTSText != "" {
		h.sendChatAction(ctx, msg.Chat.ID, msg.IsUserbot, "record_voice")
		audioData, err := h.voiceService.GenerateVoice(ctx, resp.VoiceResponse.TTSText)
		if err == nil && len(audioData) > 0 {
			h.sendVoice(ctx, msg, audioData)
			if resp.ReplyText != "" {
				h.sendMessage(ctx, msg, resp.ReplyText)
			}
			return
		}
	}

	// Sanitize reply text
	resp.ReplyText = sanitizeReplyText(resp.ReplyText)

	// Send normal text reply
	if resp.ReplyText != "" {
		h.sendMessage(ctx, msg, resp.ReplyText)
	}
}

func sanitizeReplyText(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}

	// Strip hallucinated Fish Audio bracket tags like [flirty], [giggle], [whisper], [soft], etc.
	tagRegex := regexp.MustCompile(`\[[a-zA-Z0-9_\-\s]+\]`)
	t = tagRegex.ReplaceAllString(t, "")

	// Strip asterisk roleplay actions like *smiles*, *laughs*, *blushes*
	actionRegex := regexp.MustCompile(`\*[a-zA-Z0-9_\-\s]+\*`)
	t = actionRegex.ReplaceAllString(t, "")

	// Check for repeating bracket loops or runaway hallucinated tags (e.g. [2023][2023]...)
	if strings.Count(t, "[") > 2 {
		var cleanLines []string
		for _, line := range strings.Split(t, "\n") {
			l := strings.TrimSpace(line)
			if strings.Count(l, "[") > 2 || strings.Contains(l, "[202") {
				continue
			}
			cleanLines = append(cleanLines, l)
		}
		t = strings.TrimSpace(strings.Join(cleanLines, "\n"))
	}

	// Telegram limit safeguard
	t = strings.TrimSpace(t)
	if len(t) > 2000 {
		t = strings.TrimSpace(t[:2000])
	}
	return t
}

func (h *ConversationHandler) sendMessage(ctx context.Context, msg *domain.TelegramMessage, text string) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	_ = h.historyRepo.AddMessage(ctx, chatIDStr, "assistant", h.botUsername, text, "", strconv.Itoa(msg.MessageID))

	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		log.Printf("[Conversation] Sending reply from Userbot MTProto account to chat %d...", msg.Chat.ID)
		err := h.userbotSender.SendMessage(ctx, msg.Chat.ID, text, msg.MessageID)
		if err == nil {
			return
		}
		log.Printf("⚠️ [Conversation] Userbot SendMessage failed: %v. Falling back to bot API...", err)
	}

	_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, text, telegram.SendMessageOptions{
		ReplyToID: msg.MessageID,
	})
}

func (h *ConversationHandler) sendPhoto(ctx context.Context, msg *domain.TelegramMessage, photoData []byte, photoURL, caption string) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	photoRecordText := caption
	if photoRecordText == "" {
		photoRecordText = "[Sent photo]"
	}
	_ = h.historyRepo.AddMessage(ctx, chatIDStr, "assistant", h.botUsername, photoRecordText, "", strconv.Itoa(msg.MessageID))

	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		log.Printf("[Conversation] Sending photo from Userbot MTProto account to chat %d...", msg.Chat.ID)
		err := h.userbotSender.SendPhoto(ctx, msg.Chat.ID, photoData, photoURL, caption, msg.MessageID)
		if err == nil {
			return
		}
		log.Printf("⚠️ [Conversation] Userbot SendPhoto failed: %v. Falling back to bot API...", err)
	}

	if len(photoData) > 0 {
		_, _ = h.botClient.SendPhoto(ctx, msg.Chat.ID, photoData, "", caption, msg.MessageID, "HTML")
	} else if photoURL != "" {
		_, _ = h.botClient.SendPhoto(ctx, msg.Chat.ID, nil, photoURL, caption, msg.MessageID, "HTML")
	}
}

func (h *ConversationHandler) sendVoice(ctx context.Context, msg *domain.TelegramMessage, voiceData []byte) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	_ = h.historyRepo.AddMessage(ctx, chatIDStr, "assistant", h.botUsername, "[Sent voice message]", "", strconv.Itoa(msg.MessageID))

	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		log.Printf("[Conversation] Sending voice from Userbot MTProto account to chat %d...", msg.Chat.ID)
		err := h.userbotSender.SendVoice(ctx, msg.Chat.ID, voiceData, msg.MessageID)
		if err == nil {
			return
		}
		log.Printf("⚠️ [Conversation] Userbot SendVoice failed: %v. Falling back to bot API...", err)
	}

	_, _ = h.botClient.SendVoice(ctx, msg.Chat.ID, voiceData, msg.MessageID)
}

func (h *ConversationHandler) setReaction(ctx context.Context, msg *domain.TelegramMessage, emoji string) {
	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		_ = h.userbotSender.SetReaction(ctx, msg.Chat.ID, msg.MessageID, emoji)
		return
	}

	h.botClient.SetMessageReaction(ctx, msg.Chat.ID, msg.MessageID, emoji)
}

func (h *ConversationHandler) forwardToAdmin(ctx context.Context, msg *domain.TelegramMessage, msgText, adminUname string) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	admins, err := h.adminRepo.GetGroupAdmins(ctx, chatIDStr)
	if err != nil || len(admins) == 0 {
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "⚠️ No administrators registered for this group yet.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		return
	}

	var targetAdmin *domain.AdminInfo
	if adminUname != "" {
		clean := strings.ToLower(strings.TrimPrefix(adminUname, "@"))
		for _, a := range admins {
			if strings.ToLower(a.Username) == clean {
				targetAdmin = &a
				break
			}
		}
	}
	if targetAdmin == nil {
		targetAdmin = &admins[0]
	}

	privateChatID, err := h.adminRepo.GetAdminPrivateChatID(ctx, targetAdmin.UserID)
	if err == nil && privateChatID != "" {
		senderName := msg.From.FirstName
		if msg.From.Username != "" {
			senderName = "@" + msg.From.Username
		}
		dmText := fmt.Sprintf("📩 <b>Support Forward from %s</b> in group <b>%s</b>:\n\n%s",
			senderName, msg.Chat.Title, msgText)

		_, _ = h.botClient.SendMessage(ctx, privateChatID, dmText, telegram.SendMessageOptions{ParseMode: "HTML"})
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "✅ Your message has been forwarded to the administrator!", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
	} else {
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "⚠️ Administrator has not started a private chat with the bot yet.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
	}
}

func (h *ConversationHandler) executeAdminTool(ctx context.Context, msg *domain.TelegramMessage, name string, args map[string]any) {
	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIdent, _ := args["user_identifier"].(string)
	reason, _ := args["reason"].(string)

	targetUID := userIdent
	targetUname := userIdent
	if uID, uName, err := h.historyRepo.ResolveUserIdentifier(ctx, chatIDStr, userIdent); err == nil && uID != "" {
		targetUID = uID
		targetUname = uName
	}

	switch name {
	case "mute_user":
		mins := 15
		if dur, ok := args["duration_minutes"].(float64); ok && dur > 0 {
			mins = int(dur)
		}
		until := time.Now().Add(time.Duration(mins) * time.Minute).Unix()
		f := false
		_ = h.botClient.RestrictChatMember(ctx, msg.Chat.ID, targetUID, telegram.ChatPermissions{
			CanSendMessages: &f,
		}, until)
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "mute", reason, mins, msg.From.Username)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔇 <b>%s</b> has been muted for %d minutes.\nReason: %s", targetUname, mins, reason), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "ban_user":
		_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, targetUID, 0, true)
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "ban", reason, 0, msg.From.Username)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔨 <b>%s</b> has been banned.\nReason: %s", targetUname, reason), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "unban_user":
		_ = h.botClient.UnbanChatMember(ctx, msg.Chat.ID, targetUID, true)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("✅ <b>%s</b> has been unbanned.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "unmute_user":
		t := true
		_ = h.botClient.RestrictChatMember(ctx, msg.Chat.ID, targetUID, telegram.ChatPermissions{
			CanSendMessages: &t,
		}, 0)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔊 <b>%s</b> has been unmuted.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "warn_user":
		count, _ := h.warningRepo.AddWarning(ctx, chatIDStr, targetUID, targetUname, reason, msg.From.Username)
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "warn", reason, 0, msg.From.Username)
		if count >= 3 {
			_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, targetUID, 0, true)
			_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUID)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🚨 <b>%s</b> reached 3/3 warnings and was <b>BANNED</b>.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})
		} else {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("⚠️ Warning (%d/3) for <b>%s</b>.\nReason: %s", count, targetUname, reason), telegram.SendMessageOptions{ParseMode: "HTML"})
		}

	case "clear_warnings":
		_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUID)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("✅ Warnings cleared for <b>%s</b>.", targetUname), telegram.SendMessageOptions{ParseMode: "HTML"})

	case "purge_messages":
		num := 5
		if n, ok := args["num_messages"].(float64); ok && n > 0 {
			num = int(n)
		}
		for i := 0; i <= num; i++ {
			_ = h.botClient.DeleteMessage(ctx, msg.Chat.ID, msg.MessageID-i)
		}
	}
}
