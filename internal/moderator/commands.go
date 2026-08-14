package moderator

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"telegram-ai-assistant/internal/ai"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/telegram"
)

type CommandHandler struct {
	groupRepo   domain.GroupRepository
	adminRepo   domain.AdminRepository
	warningRepo domain.WarningRepository
	modLogRepo  domain.ModerationLogRepository
	historyRepo domain.HistoryRepository
	karmaRepo   domain.KarmaRepository
	shipRepo    domain.ShipRepository
	aiClient    *ai.Client
	botClient   *telegram.BotClient
}

func NewCommandHandler(
	groupRepo domain.GroupRepository,
	adminRepo domain.AdminRepository,
	warningRepo domain.WarningRepository,
	modLogRepo domain.ModerationLogRepository,
	historyRepo domain.HistoryRepository,
	karmaRepo domain.KarmaRepository,
	shipRepo domain.ShipRepository,
	aiClient *ai.Client,
	botClient *telegram.BotClient,
) *CommandHandler {
	return &CommandHandler{
		groupRepo:   groupRepo,
		adminRepo:   adminRepo,
		warningRepo: warningRepo,
		modLogRepo:  modLogRepo,
		historyRepo: historyRepo,
		karmaRepo:   karmaRepo,
		shipRepo:    shipRepo,
		aiClient:    aiClient,
		botClient:   botClient,
	}
}

func (h *CommandHandler) HandleCommand(ctx context.Context, msg *domain.TelegramMessage, isAdmin bool) bool {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}
	if !strings.HasPrefix(text, "/") {
		return false
	}

	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])
	cmd = strings.Split(cmd, "@")[0] // strip bot username if present e.g. /warn@bot

	args := parts[1:]

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	senderIDStr := fmt.Sprintf("%d", msg.From.ID)

	switch cmd {
	case "/start":
		if msg.Chat.Type == "private" {
			_ = h.adminRepo.RegisterAdminPrivateChat(ctx, senderIDStr, chatIDStr)
			startText := "👋 Hello! I am your AI Group Assistant & Moderator.\n\n✅ If you are a group admin, your private chat is now connected to receive message forwards and alerts!"
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, startText, telegram.SendMessageOptions{ParseMode: "HTML"})
			return true
		}
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "🤖 Janvi AI Assistant is active in this group!", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		return true

	case "/rules":
		group, err := h.groupRepo.GetGroup(ctx, chatIDStr)
		if err == nil && group != nil && group.Rules != "" {
			ruleText := fmt.Sprintf("📜 <b>Group Rules for %s:</b>\n\n%s", msg.Chat.Title, group.Rules)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, ruleText, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		} else {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "ℹ️ No rules have been configured for this group yet. Admins can use <code>/setrules [text]</code>", telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		}
		return true

	case "/setrules":
		if !isAdmin {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "🚫 Only administrators can update group rules.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
			return true
		}
		if len(args) == 0 {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Usage: <code>/setrules [your rules here]</code>", telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
			return true
		}
		newRules := strings.Join(args, " ")
		_ = h.groupRepo.UpdateRules(ctx, chatIDStr, newRules)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "✅ Group rules updated successfully!", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		return true

	case "/warn":
		if !isAdmin {
			return true
		}
		targetUserID, targetUsername, reason := h.extractTargetAndReason(ctx, msg, args)
		if targetUserID == "" {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Usage: <code>/warn @user [reason]</code> or reply to a message with <code>/warn [reason]</code>", telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
			return true
		}

		count, _ := h.warningRepo.AddWarning(ctx, chatIDStr, targetUserID, targetUsername, reason, msg.From.Username)
		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUserID, targetUsername, "warn", reason, 0, msg.From.Username)

		if count >= 3 {
			// Auto ban after 3 warnings
			_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, targetUserID, 0, true)
			_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUserID)
			banNotice := fmt.Sprintf("🚨 <b>%s</b> has received 3/3 warnings and has been <b>BANNED</b> from the group.\nReason: %s", targetUsername, reason)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, banNotice, telegram.SendMessageOptions{ParseMode: "HTML"})
		} else {
			warnNotice := fmt.Sprintf("⚠️ <b>Warning (%d/3)</b> for <b>%s</b>!\nReason: %s", count, targetUsername, reason)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, warnNotice, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		}
		return true

	case "/clearwarns":
		if !isAdmin {
			return true
		}
		targetUserID, targetUsername, _ := h.extractTargetAndReason(ctx, msg, args)
		if targetUserID == "" {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Usage: <code>/clearwarns @user</code>", telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
			return true
		}
		_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUserID)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("✅ Warnings cleared for <b>%s</b>.", targetUsername), telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		return true

	case "/mute":
		if !isAdmin {
			return true
		}
		targetUserID, targetUsername, rest := h.extractTargetAndReason(ctx, msg, args)
		if targetUserID == "" {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Usage: <code>/mute @user [minutes] [reason]</code>", telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
			return true
		}
		mins := 15
		reason := "Violating group rules"
		restParts := strings.Fields(rest)
		if len(restParts) > 0 {
			if parsedMins, err := strconv.Atoi(restParts[0]); err == nil && parsedMins > 0 {
				mins = parsedMins
				reason = strings.Join(restParts[1:], " ")
			} else {
				reason = rest
			}
		}
		if reason == "" {
			reason = "Violating group rules"
		}

		untilDate := time.Now().Add(time.Duration(mins) * time.Minute).Unix()
		f := false
		_ = h.botClient.RestrictChatMember(ctx, msg.Chat.ID, targetUserID, telegram.ChatPermissions{
			CanSendMessages:       &f,
			CanSendAudios:         &f,
			CanSendDocuments:      &f,
			CanSendPhotos:         &f,
			CanSendVideos:         &f,
			CanSendOtherMessages:  &f,
			CanAddWebPagePreviews: &f,
		}, untilDate)

		_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUserID, targetUsername, "mute", reason, mins, msg.From.Username)
		muteText := fmt.Sprintf("🔇 <b>%s</b> has been muted for <b>%d minutes</b>.\nReason: %s", targetUsername, mins, reason)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, muteText, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		return true

	case "/unmute":
		if !isAdmin {
			return true
		}
		targetUserID, targetUsername, _ := h.extractTargetAndReason(ctx, msg, args)
		if targetUserID != "" {
			t := true
			_ = h.botClient.RestrictChatMember(ctx, msg.Chat.ID, targetUserID, telegram.ChatPermissions{
				CanSendMessages:       &t,
				CanSendAudios:         &t,
				CanSendDocuments:      &t,
				CanSendPhotos:         &t,
				CanSendVideos:         &t,
				CanSendOtherMessages:  &t,
				CanAddWebPagePreviews: &t,
			}, 0)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔊 <b>%s</b> has been unmuted.", targetUsername), telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		}
		return true

	case "/ban":
		if !isAdmin {
			return true
		}
		targetUserID, targetUsername, reason := h.extractTargetAndReason(ctx, msg, args)
		if targetUserID != "" {
			if reason == "" {
				reason = "Banned by administrator"
			}
			_ = h.botClient.BanChatMember(ctx, msg.Chat.ID, targetUserID, 0, true)
			_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUserID, targetUsername, "ban", reason, 0, msg.From.Username)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🔨 <b>%s</b> has been banned from the group.\nReason: %s", targetUsername, reason), telegram.SendMessageOptions{ParseMode: "HTML"})
		}
		return true

	case "/unban":
		if !isAdmin {
			return true
		}
		targetUserID, targetUsername, _ := h.extractTargetAndReason(ctx, msg, args)
		if targetUserID != "" {
			_ = h.botClient.UnbanChatMember(ctx, msg.Chat.ID, targetUserID, true)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("✅ <b>%s</b> has been unbanned.", targetUsername), telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		}
		return true

	case "/purge", "/del":
		if !isAdmin {
			return true
		}
		numToDelete := 1
		if len(args) > 0 {
			if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
				numToDelete = n
			}
		}
		if numToDelete > 100 {
			numToDelete = 100
		}

		curID := msg.MessageID
		for i := 0; i <= numToDelete; i++ {
			_ = h.botClient.DeleteMessage(ctx, msg.Chat.ID, curID-i)
		}
		return true

	case "/karma":
		points, _ := h.karmaRepo.GetKarma(ctx, chatIDStr, senderIDStr)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("🌟 You currently have <b>%d karma points</b> in this group.", points), telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		return true

	case "/topkarma":
		topList, err := h.karmaRepo.GetTopKarma(ctx, chatIDStr, 10)
		if err == nil && len(topList) > 0 {
			var sb strings.Builder
			sb.WriteString("🏆 <b>Top Karma Leaderboard:</b>\n\n")
			for i, k := range topList {
				sb.WriteString(fmt.Sprintf("%d. <b>%s</b> — %d points\n", i+1, k.Username, k.Points))
			}
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, sb.String(), telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		} else {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "No karma points recorded yet!", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		}
		return true

	case "/summarize", "/summarise":
		recent, err := h.historyRepo.GetRecentMessages(ctx, chatIDStr, 30)
		if err != nil || len(recent) == 0 {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Not enough recent conversation history to summarize.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
			return true
		}

		var histSb strings.Builder
		for _, m := range recent {
			histSb.WriteString(fmt.Sprintf("%s: %s\n", m.Username, m.MessageText))
		}

		summaryPrompt := []domain.ChatMessage{
			{
				Role:    "system",
				Content: "You are an intelligent group summarizer. Summarize the key discussion topics, highlights, and decisions from the chat history in 3-5 concise bullet points in natural Hinglish. Keep it fun and clear.",
			},
			{
				Role:    "user",
				Content: histSb.String(),
			},
		}

		res, err := h.aiClient.ChatCompletions(ctx, summaryPrompt, ai.ChatCompletionOptions{})
		if err == nil && res != nil {
			summaryText := fmt.Sprintf("📋 <b>Group Conversation Summary:</b>\n\n%s", res.Message.GetStringContent())
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, summaryText, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		} else {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Failed to generate summary.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		}
		return true

	case "/ship":
		today := time.Now().Format("2006-01-02")
		existing, _ := h.shipRepo.GetDailyShip(ctx, chatIDStr, today)
		if existing != nil {
			shipText := fmt.Sprintf("💘 <b>Today's Couple of the Day:</b>\n\n👩‍❤️‍👨 <b>%s</b> ❤️ <b>%s</b>\n\nMay your bond grow stronger today! ✨",
				existing.User1Name, existing.User2Name)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, shipText, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
			return true
		}

		recent, err := h.historyRepo.GetRecentMessages(ctx, chatIDStr, 50)
		if err == nil && len(recent) >= 2 {
			usersMap := make(map[string]string)
			for _, m := range recent {
				if m.UserID != "" && m.Username != "" {
					usersMap[m.UserID] = m.Username
				}
			}

			var userList []struct{ ID, Name string }
			for id, name := range usersMap {
				userList = append(userList, struct{ ID, Name string }{id, name})
			}

			if len(userList) >= 2 {
				rand.Shuffle(len(userList), func(i, j int) { userList[i], userList[j] = userList[j], userList[i] })
				u1 := userList[0]
				u2 := userList[1]

				ship := &domain.DailyShip{
					ChatID:    chatIDStr,
					User1ID:   u1.ID,
					User1Name: u1.Name,
					User2ID:   u2.ID,
					User2Name: u2.Name,
					ShipDate:  today,
				}
				_ = h.shipRepo.SaveDailyShip(ctx, ship)

				shipText := fmt.Sprintf("💘 <b>Today's Couple of the Day:</b>\n\n👩‍❤️‍👨 <b>%s</b> ❤️ <b>%s</b>\n\nMay your bond grow stronger today! ✨",
					u1.Name, u2.Name)
				_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, shipText, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
				return true
			}
		}

		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Not enough active members in chat history to generate a ship!", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		return true

	case "/help":
		helpText := `🤖 <b>Janvi Assistant Commands:</b>

<b>General Commands:</b>
• <code>/rules</code> - View group rules
• <code>/karma</code> - Check your karma score
• <code>/topkarma</code> - View karma leaderboard
• <code>/summarize</code> - AI summary of recent chat
• <code>/ship</code> - Generate daily couple of the day

<b>Admin Commands:</b>
• <code>/warn @user [reason]</code> - Warn user (3 warnings = auto-ban)
• <code>/clearwarns @user</code> - Reset warnings for user
• <code>/mute @user [minutes] [reason]</code> - Mute user
• <code>/unmute @user</code> - Unmute user
• <code>/ban @user [reason]</code> - Ban user
• <code>/unban @user</code> - Unban user
• <code>/setrules [rules]</code> - Update group rules
• <code>/purge [number]</code> - Delete recent messages

💡 <i>You can also mention Janvi naturally in chat or ask her for selfies and voice notes!</i>`
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, helpText, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
		return true
	}

	return false
}

func (h *CommandHandler) extractTargetAndReason(ctx context.Context, msg *domain.TelegramMessage, args []string) (userID, username, reason string) {
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		target := msg.ReplyToMessage.From
		userID = fmt.Sprintf("%d", target.ID)
		username = target.Username
		if username == "" {
			username = target.FirstName
		}
		reason = strings.Join(args, " ")
		return userID, username, reason
	}

	if len(args) > 0 {
		identifier := args[0]
		reason = strings.Join(args[1:], " ")
		chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
		uID, uName, err := h.historyRepo.ResolveUserIdentifier(ctx, chatIDStr, identifier)
		if err == nil && uID != "" {
			return uID, uName, reason
		}
		return "", identifier, reason
	}

	return "", "", ""
}
