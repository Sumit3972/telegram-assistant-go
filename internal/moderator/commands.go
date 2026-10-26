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
	groupRepo     domain.GroupRepository
	adminRepo     domain.AdminRepository
	warningRepo   domain.WarningRepository
	modLogRepo    domain.ModerationLogRepository
	historyRepo   domain.HistoryRepository
	karmaRepo     domain.KarmaRepository
	shipRepo      domain.ShipRepository
	blockedRepo   domain.BlockedUserRepository
	abuseRepo     domain.AbuseRecordRepository
	aiClient      *ai.Client
	botClient     *telegram.BotClient
	botName       string
	userbotSender domain.UserbotSender
}

func NewCommandHandler(
	groupRepo domain.GroupRepository,
	adminRepo domain.AdminRepository,
	warningRepo domain.WarningRepository,
	modLogRepo domain.ModerationLogRepository,
	historyRepo domain.HistoryRepository,
	karmaRepo domain.KarmaRepository,
	shipRepo domain.ShipRepository,
	blockedRepo domain.BlockedUserRepository,
	abuseRepo domain.AbuseRecordRepository,
	aiClient *ai.Client,
	botClient *telegram.BotClient,
	botName ...string,
) *CommandHandler {
	name := "Chavi"
	if len(botName) > 0 && botName[0] != "" {
		name = botName[0]
	}
	return &CommandHandler{
		groupRepo:   groupRepo,
		adminRepo:   adminRepo,
		warningRepo: warningRepo,
		modLogRepo:  modLogRepo,
		historyRepo: historyRepo,
		karmaRepo:   karmaRepo,
		shipRepo:    shipRepo,
		blockedRepo: blockedRepo,
		abuseRepo:   abuseRepo,
		aiClient:    aiClient,
		botClient:   botClient,
		botName:     name,
	}
}

func (h *CommandHandler) SetUserbotSender(s domain.UserbotSender) {
	h.userbotSender = s
}

func (h *CommandHandler) reply(ctx context.Context, msg *domain.TelegramMessage, text string) {
	if msg.IsUserbot && h.userbotSender != nil && h.userbotSender.IsAvailable() {
		cleanText := strings.ReplaceAll(text, "<b>", "")
		cleanText = strings.ReplaceAll(cleanText, "</b>", "")
		cleanText = strings.ReplaceAll(cleanText, "<i>", "")
		cleanText = strings.ReplaceAll(cleanText, "</i>", "")
		cleanText = strings.ReplaceAll(cleanText, "<code>", "`")
		cleanText = strings.ReplaceAll(cleanText, "</code>", "`")
		cleanText = strings.ReplaceAll(cleanText, "&lt;", "<")
		cleanText = strings.ReplaceAll(cleanText, "&gt;", ">")
		cleanText = strings.ReplaceAll(cleanText, "&amp;", "&")

		err := h.userbotSender.SendMessage(ctx, msg.Chat.ID, cleanText, msg.MessageID)
		if err == nil {
			return
		}
	}

	_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, text, telegram.SendMessageOptions{
		ParseMode: "HTML",
		ReplyToID: msg.MessageID,
	})
}

func (h *CommandHandler) HandleCommand(ctx context.Context, msg *domain.TelegramMessage, isAdmin bool) bool {
	if IsOwnerOrMom(msg) {
		isAdmin = true
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}
	if !strings.HasPrefix(text, "/") && !strings.HasPrefix(text, ".") {
		return false
	}

	cmdText := text
	if strings.HasPrefix(cmdText, ".") {
		cmdText = "/" + strings.TrimPrefix(cmdText, ".")
	}

	parts := strings.Fields(cmdText)
	cmd := strings.ToLower(parts[0])
	cmd = strings.Split(cmd, "@")[0] // strip bot username if present e.g. /warn@bot

	args := parts[1:]

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	senderIDStr := fmt.Sprintf("%d", msg.From.ID)

	switch cmd {
	case "/start":
		if msg.Chat.Type == "private" {
			_ = h.adminRepo.RegisterAdminPrivateChat(ctx, senderIDStr, chatIDStr)
			startText := fmt.Sprintf("👋 Hey! Main <b>%s</b> hoon.\n\n✅ Group admins can link their private chat here to receive alerts and message reports.", h.botName)
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, startText, telegram.SendMessageOptions{ParseMode: "HTML"})
			return true
		}
		groupGreeting := fmt.Sprintf("✨ Hey everyone! <b>%s</b> is active here.", h.botName)
		_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, groupGreeting, telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
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

	case "/blocked", "/blocklist", "/blockedlist":
		if !IsOwnerOrMom(msg) && !isAdmin {
			h.reply(ctx, msg, "⛔ <b>Access Denied:</b> This command is strictly reserved for the bot owner (@Sumit3972 / Mom / Devin).")
			return true
		}

		blockedList, err := h.blockedRepo.GetBlockedUsers(ctx)
		if err != nil || len(blockedList) == 0 {
			h.reply(ctx, msg, "📋 <b>Blocked Users List:</b>\n\n✅ <i>No users are currently blocked!</i>")
			return true
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 <b>Blocked Users List (%d total):</b>\n\n", len(blockedList)))
		for i, b := range blockedList {
			t := time.Unix(b.CreatedAt, 0).Format("02 Jan 15:04 MST")
			uname := b.Username
			if uname == "" {
				uname = "None"
			} else if !strings.HasPrefix(uname, "@") {
				uname = "@" + uname
			}
			fullName := strings.TrimSpace(b.FirstName + " " + b.LastName)
			if fullName == "" {
				fullName = "User"
			}
			sb.WriteString(fmt.Sprintf("<b>%d. %s</b> (%s)\n• <b>ID:</b> <code>%s</code> | <b>Strikes:</b> %d\n• <b>Reason:</b> <i>%s</i>\n• <b>Date:</b> %s\n\n",
				i+1, fullName, uname, b.UserID, b.StrikeCount, b.Reason, t))
		}
		sb.WriteString("👉 <i>To unblock:</i> <code>/unblock &lt;username or name or ID&gt;</code>")
		h.reply(ctx, msg, sb.String())
		return true

	case "/unblock":
		if !IsOwnerOrMom(msg) && !isAdmin {
			h.reply(ctx, msg, "⛔ <b>Access Denied:</b> This command is strictly reserved for the bot owner (@Sumit3972 / Mom / Devin).")
			return true
		}

		targetQuery := strings.TrimSpace(strings.Join(args, " "))
		if targetQuery == "" && (msg.ReplyToMessage == nil || msg.ReplyToMessage.From == nil) {
			usageText := "ℹ️ <b>Usage:</b> <code>/unblock &lt;username, name, or userID&gt;</code>\n" +
				"Example: <code>/unblock @Lumi</code> or <code>/unblock Lumi</code> or <code>/unblock 8967976757</code>\n\n" +
				"<i>Or reply to any message from that user with <code>/unblock</code>.</i>"
			h.reply(ctx, msg, usageText)
			return true
		}

		cleanQuery := strings.TrimPrefix(targetQuery, "@")
		blockedList, _ := h.blockedRepo.GetBlockedUsers(ctx)

		var targetUID, targetUname, targetName, targetChatID string
		for _, b := range blockedList {
			if b.UserID == cleanQuery {
				targetUID = b.UserID
				targetUname = b.Username
				targetName = strings.TrimSpace(b.FirstName + " " + b.LastName)
				targetChatID = b.ChatID
				break
			}
			if strings.EqualFold(strings.TrimPrefix(b.Username, "@"), cleanQuery) {
				targetUID = b.UserID
				targetUname = b.Username
				targetName = strings.TrimSpace(b.FirstName + " " + b.LastName)
				targetChatID = b.ChatID
				break
			}
			if strings.EqualFold(b.FirstName, cleanQuery) || strings.EqualFold(strings.TrimSpace(b.FirstName+" "+b.LastName), cleanQuery) {
				targetUID = b.UserID
				targetUname = b.Username
				targetName = strings.TrimSpace(b.FirstName + " " + b.LastName)
				targetChatID = b.ChatID
				break
			}
		}

		if targetUID == "" && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
			targetUID = fmt.Sprintf("%d", msg.ReplyToMessage.From.ID)
			targetUname = msg.ReplyToMessage.From.Username
			targetName = msg.ReplyToMessage.From.FirstName
		}

		if targetUID == "" && cleanQuery != "" {
			if _, err := strconv.ParseInt(cleanQuery, 10, 64); err == nil {
				targetUID = cleanQuery
				targetUname = cleanQuery
				targetName = cleanQuery
			} else {
				if uID, uName, err := h.historyRepo.ResolveUserIdentifier(ctx, chatIDStr, cleanQuery); err == nil && uID != "" {
					targetUID = uID
					targetUname = uName
					targetName = uName
				}
			}
		}

		if targetUID == "" && cleanQuery != "" && h.userbotSender != nil && h.userbotSender.IsAvailable() {
			if uID, uName, err := h.userbotSender.ResolveUsername(ctx, cleanQuery); err == nil && uID > 0 {
				targetUID = fmt.Sprintf("%d", uID)
				targetUname = cleanQuery
				targetName = uName
			}
		}

		if targetUID == "" {
			h.reply(ctx, msg, fmt.Sprintf("⚠️ No user found matching <code>%s</code>.\nUse <code>/blocked</code> to view the list of currently blocked users.", targetQuery))
			return true
		}

		// 1. Remove from blocked_users database table
		if h.blockedRepo != nil {
			_ = h.blockedRepo.UnblockUser(ctx, targetChatID, targetUID)
			_ = h.blockedRepo.UnblockUser(ctx, "", targetUID)
		}

		// 2. Clear abuse strikes from abuse_records table
		if h.abuseRepo != nil {
			_ = h.abuseRepo.ClearAbuseStrikes(ctx, targetUID)
		}

		// 3. Clear warnings from warnings table
		if h.warningRepo != nil {
			_ = h.warningRepo.ClearWarnings(ctx, targetChatID, targetUID)
			_ = h.warningRepo.ClearWarnings(ctx, chatIDStr, targetUID)
		}

		// 4. Native Telegram Unblock
		uidInt, _ := strconv.ParseInt(targetUID, 10, 64)
		if uidInt != 0 {
			// MTProto Userbot ContactsUnblock
			if h.userbotSender != nil && h.userbotSender.IsAvailable() {
				_ = h.userbotSender.UnblockUser(ctx, uidInt)
			}
			// Bot API Unban in group if applicable
			if msg.Chat.ID != 0 && msg.Chat.Type != "private" {
				_ = h.botClient.UnbanChatMember(ctx, msg.Chat.ID, uidInt, true)
			}
			if cidInt, err := strconv.ParseInt(targetChatID, 10, 64); err == nil && cidInt != 0 {
				_ = h.botClient.UnbanChatMember(ctx, cidInt, uidInt, true)
			}
		}

		// 5. Audit Log
		operator := "Owner"
		if msg.From != nil {
			if msg.From.Username != "" {
				operator = "@" + msg.From.Username
			} else if msg.From.FirstName != "" {
				operator = msg.From.FirstName
			}
		}
		if h.modLogRepo != nil {
			_ = h.modLogRepo.AddLog(ctx, chatIDStr, targetUID, targetUname, "unblock", fmt.Sprintf("Manual unblock by %s", operator), 0, operator)
		}

		if targetName == "" {
			targetName = targetUID
		}
		if targetUname == "" {
			targetUname = "None"
		} else if !strings.HasPrefix(targetUname, "@") {
			targetUname = "@" + targetUname
		}

		successMsg := fmt.Sprintf(`✅ <b>User Successfully Unblocked!</b>

👤 <b>User:</b> %s (%s)
🆔 <b>User ID:</b> <code>%s</code>

🔄 <b>Limits & Access Restored:</b>
• Removed from blocked users blacklist
• Abuse strikes reset to 0
• Warnings count reset to 0/3
• Unblocked on Telegram MTProto & Group`, targetName, targetUname, targetUID)

		h.reply(ctx, msg, successMsg)
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

		maxTokens := 40000
		res, err := h.aiClient.ChatCompletions(ctx, summaryPrompt, ai.ChatCompletionOptions{MaxTokens: &maxTokens})
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

	case "/poll":
		rawArgs := strings.TrimSpace(strings.TrimPrefix(text, parts[0]))
		if rawArgs == "" {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "Usage: <code>/poll Question? | Option 1 | Option 2 [| Option 3...]</code>", telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
			return true
		}

		pollParts := strings.Split(rawArgs, "|")
		if len(pollParts) < 3 {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "❌ Poll requires a question and at least 2 options separated by '|'. Example: <code>/poll Chai ya Coffee? | Chai ☕ | Coffee 🧋</code>", telegram.SendMessageOptions{ParseMode: "HTML", ReplyToID: msg.MessageID})
			return true
		}

		question := strings.TrimSpace(pollParts[0])
		var options []string
		for _, opt := range pollParts[1:] {
			trimmed := strings.TrimSpace(opt)
			if trimmed != "" {
				options = append(options, trimmed)
			}
		}

		if len(options) < 2 {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, "❌ Poll requires at least 2 valid options.", telegram.SendMessageOptions{ReplyToID: msg.MessageID})
			return true
		}

		_, err := h.botClient.SendPoll(ctx, msg.Chat.ID, question, options, false, false, msg.MessageID)
		if err != nil {
			_, _ = h.botClient.SendMessage(ctx, msg.Chat.ID, fmt.Sprintf("Failed to create poll: %v", err), telegram.SendMessageOptions{ReplyToID: msg.MessageID})
		}
		return true

	case "/help":
		botLabel := h.botName
		if botLabel == "" {
			botLabel = "Assistant"
		}
		helpText := fmt.Sprintf(`🤖 <b>%s Commands:</b>

<b>General Commands:</b>
• <code>/rules</code> - View group rules
• <code>/karma</code> - Check your karma score
• <code>/topkarma</code> - View karma leaderboard
• <code>/summarize</code> - AI summary of recent chat
• <code>/ship</code> - Generate daily couple of the day
• <code>/poll Question | Opt1 | Opt2</code> - Create interactive poll

<b>Admin Commands:</b>
• <code>/warn @user [reason]</code> - Warn user (3 warnings = auto-ban)
• <code>/clearwarns @user</code> - Reset warnings for user
• <code>/mute @user [minutes] [reason]</code> - Mute user
• <code>/unmute @user</code> - Unmute user
• <code>/ban @user [reason]</code> - Ban user
• <code>/unban @user</code> - Unban user
• <code>/setrules [rules]</code> - Update group rules
• <code>/purge [number]</code> - Delete recent messages

💡 <i>You can also mention @%s naturally in chat or ask for selfies, polls, and voice notes!</i>`, botLabel, botLabel)
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
