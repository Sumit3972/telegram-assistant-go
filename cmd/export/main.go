package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"telegram-ai-assistant/internal/config"
	"telegram-ai-assistant/internal/database"
)

// AIResponsePayload represents internal structured JSON responses that the AI sometimes returned
type AIResponsePayload struct {
	ReplyText     string `json:"reply_text"`
	VoiceScript   string `json:"voice_script,omitempty"`
	ReactionEmoji string `json:"reaction_emoji,omitempty"`
}

// ChatMessage represents a single message from either user or AI
type ChatMessage struct {
	ID                int64  `json:"id"`
	ChatID            string `json:"chat_id"`
	UserID            string `json:"user_id"`
	Username          string `json:"username"`
	SenderDisplayName string `json:"sender_display_name"`
	Role              string `json:"role"` // "assistant" or "user"
	MessageText       string `json:"message_text"`
	RawText           string `json:"raw_text,omitempty"`
	VoiceScript       string `json:"voice_script,omitempty"`
	ReactionEmoji     string `json:"reaction_emoji,omitempty"`
	MessageID         string `json:"message_id,omitempty"`
	ReplyToMessageID  string `json:"reply_to_message_id,omitempty"`
	CreatedAt         int64  `json:"timestamp"`
	DateTime          string `json:"datetime"`
}

// ChatSession represents a full conversation thread grouped by Telegram chat
type ChatSession struct {
	ChatID       string         `json:"chat_id"`
	ChatTitle    string         `json:"chat_title"`
	ChatType     string         `json:"chat_type"` // "group" or "private"
	MessageCount int            `json:"message_count"`
	FirstMessage string         `json:"first_message_time"`
	LastMessage  string         `json:"last_message_time"`
	Messages     []*ChatMessage `json:"messages"`
}

// ExportData represents the complete JSON export payload
type ExportData struct {
	ExportedAt       string         `json:"exported_at"`
	TotalMessages    int            `json:"total_messages"`
	TotalChats       int            `json:"total_chats"`
	AIMessages       int            `json:"ai_messages_count"`
	UserMessages     int            `json:"user_messages_count"`
	Chats            []*ChatSession `json:"chats"`
	AllMessagesFlat  []*ChatMessage `json:"all_messages_chronological"`
}

func parseMessageContent(raw string, role string) (cleanText, rawText, voiceScript, reactionEmoji string) {
	if role != "assistant" {
		return raw, "", "", ""
	}

	cleaned := strings.ReplaceAll(raw, "\\\n", "\n")
	cleaned = strings.TrimSpace(cleaned)
	if strings.HasPrefix(cleaned, "{") && strings.HasSuffix(cleaned, "}") {
		var payload AIResponsePayload
		if err := json.Unmarshal([]byte(cleaned), &payload); err == nil && payload.ReplyText != "" {
			return payload.ReplyText, raw, payload.VoiceScript, payload.ReactionEmoji
		}
	}
	return raw, "", "", ""
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config load failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("DB connect failed: %v", err)
	}
	defer pool.Close()

	// 1. Fetch group titles
	groupTitles := make(map[string]string)
	gRows, err := pool.Query(ctx, "SELECT chat_id, title FROM groups")
	if err == nil {
		for gRows.Next() {
			var cid, title string
			if err := gRows.Scan(&cid, &title); err == nil && strings.TrimSpace(title) != "" {
				groupTitles[cid] = strings.TrimSpace(title)
			}
		}
		gRows.Close()
	}

	// 2. Fetch user relationship details for display names
	userNames := make(map[string]string)
	rRows, err := pool.Query(ctx, "SELECT user_id, username, first_name, last_name FROM relationships")
	if err == nil {
		for rRows.Next() {
			var uid, uname, fname, lname string
			if err := rRows.Scan(&uid, &uname, &fname, &lname); err == nil {
				fullName := strings.TrimSpace(fname + " " + lname)
				if fullName != "" {
					userNames[uid] = fullName
				} else if uname != "" {
					userNames[uid] = "@" + strings.TrimPrefix(uname, "@")
				}
			}
		}
		rRows.Close()
	}

	// 3. Query all chat_history records in chronological order
	rows, err := pool.Query(ctx, `
		SELECT 
			id, 
			COALESCE(chat_id, ''), 
			COALESCE(user_id, ''), 
			COALESCE(username, ''), 
			COALESCE(message_text, ''), 
			COALESCE(message_id, ''), 
			COALESCE(reply_to_message_id, ''), 
			COALESCE(created_at, 0)
		FROM chat_history
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		log.Fatalf("Query chat_history failed: %v", err)
	}
	defer rows.Close()

	var allMessages []*ChatMessage
	chatMap := make(map[string]*ChatSession)
	aiCount := 0
	userCount := 0

	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		loc = time.Local
	}

	for rows.Next() {
		var m ChatMessage
		var rawMsgText string
		if err := rows.Scan(&m.ID, &m.ChatID, &m.UserID, &m.Username, &rawMsgText, &m.MessageID, &m.ReplyToMessageID, &m.CreatedAt); err != nil {
			log.Printf("Scan error: %v", err)
			continue
		}

		// Determine Role and Sender Display Name
		if strings.EqualFold(m.UserID, "assistant") || strings.EqualFold(m.Username, cfg.MyPersonalUsername) || strings.EqualFold(m.Username, "Chavi396") {
			m.Role = "assistant"
			aiCount++
			if m.Username == "" {
				m.Username = "Chavi396"
			}
			m.SenderDisplayName = "Chavi (AI Assistant)"
		} else {
			m.Role = "user"
			userCount++
			if name, ok := userNames[m.UserID]; ok && name != "" {
				m.SenderDisplayName = name
			} else if m.Username != "" {
				m.SenderDisplayName = m.Username
			} else {
				m.SenderDisplayName = fmt.Sprintf("User_%s", m.UserID)
			}
		}

		// Parse structured content if applicable
		cleanText, rawText, voiceScript, reactionEmoji := parseMessageContent(rawMsgText, m.Role)
		m.MessageText = cleanText
		m.RawText = rawText
		m.VoiceScript = voiceScript
		m.ReactionEmoji = reactionEmoji

		t := time.Unix(m.CreatedAt, 0).In(loc)
		m.DateTime = t.Format("2006-01-02 15:04:05 MST")

		allMessages = append(allMessages, &m)

		// Group into chat sessions
		sess, ok := chatMap[m.ChatID]
		if !ok {
			title := groupTitles[m.ChatID]
			chatType := "group"
			if title == "" {
				if name, exists := userNames[m.ChatID]; exists {
					title = fmt.Sprintf("DM with %s", name)
					chatType = "private"
				} else {
					title = fmt.Sprintf("Private Chat (%s)", m.ChatID)
					chatType = "private"
				}
			}

			sess = &ChatSession{
				ChatID:    m.ChatID,
				ChatTitle: title,
				ChatType:  chatType,
				Messages:  make([]*ChatMessage, 0),
			}
			chatMap[m.ChatID] = sess
		}

		sess.Messages = append(sess.Messages, &m)
	}

	// Sort sessions by message count descending (most active chats first)
	var sessions []*ChatSession
	for _, s := range chatMap {
		s.MessageCount = len(s.Messages)
		if len(s.Messages) > 0 {
			s.FirstMessage = s.Messages[0].DateTime
			s.LastMessage = s.Messages[len(s.Messages)-1].DateTime
		}
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].MessageCount > sessions[j].MessageCount
	})

	now := time.Now().In(loc)
	export := ExportData{
		ExportedAt:      now.Format("2006-01-02 15:04:05 MST"),
		TotalMessages:   len(allMessages),
		TotalChats:      len(sessions),
		AIMessages:      aiCount,
		UserMessages:    userCount,
		Chats:           sessions,
		AllMessagesFlat: allMessages,
	}

	// 1. Write JSON file (chat_history.json)
	jsonFile, err := os.Create("chat_history.json")
	if err != nil {
		log.Fatalf("Failed to create chat_history.json: %v", err)
	}
	defer jsonFile.Close()

	encoder := json.NewEncoder(jsonFile)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(export); err != nil {
		log.Fatalf("Failed to encode JSON: %v", err)
	}
	fmt.Printf("✅ chat_history.json created: %d messages across %d chats\n", len(allMessages), len(sessions))

	// 2. Write TXT file (chat_history.txt - UTF-8 encoded)
	txtFile, err := os.Create("chat_history.txt")
	if err != nil {
		log.Fatalf("Failed to create chat_history.txt: %v", err)
	}
	defer txtFile.Close()

	var sb strings.Builder
	sb.WriteString("================================================================================\n")
	sb.WriteString("                     TELEGRAM AI & USER CHAT EXPORT\n")
	sb.WriteString(fmt.Sprintf(" Export Date:    %s\n", export.ExportedAt))
	sb.WriteString(fmt.Sprintf(" Total Messages: %d  (AI: %d, User: %d)\n", export.TotalMessages, export.AIMessages, export.UserMessages))
	sb.WriteString(fmt.Sprintf(" Total Chats:    %d\n", export.TotalChats))
	sb.WriteString(" Encoding:       UTF-8\n")
	sb.WriteString("================================================================================\n\n")

	sb.WriteString("TABLE OF CONTENTS / CHAT INDEX:\n")
	sb.WriteString("--------------------------------------------------------------------------------\n")
	for idx, s := range sessions {
		sb.WriteString(fmt.Sprintf("%2d. [%-7s] %-40s (ID: %s) -> %d messages [%s to %s]\n",
			idx+1, s.ChatType, s.ChatTitle, s.ChatID, s.MessageCount, s.FirstMessage, s.LastMessage))
	}
	sb.WriteString("--------------------------------------------------------------------------------\n\n")

	// Print each chat session with its messages
	for idx, s := range sessions {
		sb.WriteString("================================================================================\n")
		sb.WriteString(fmt.Sprintf("CHAT %d/%d: %s\n", idx+1, len(sessions), s.ChatTitle))
		sb.WriteString(fmt.Sprintf("Chat ID:    %s\n", s.ChatID))
		sb.WriteString(fmt.Sprintf("Chat Type:  %s\n", s.ChatType))
		sb.WriteString(fmt.Sprintf("Messages:   %d (from %s to %s)\n", s.MessageCount, s.FirstMessage, s.LastMessage))
		sb.WriteString("================================================================================\n\n")

		for _, m := range s.Messages {
			roleTag := "[USER]"
			if m.Role == "assistant" {
				roleTag = "[AI]  "
			}

			replyInfo := ""
			if m.ReplyToMessageID != "" {
				replyInfo = fmt.Sprintf(" (Replying to msg #%s)", m.ReplyToMessageID)
			}

			extraMeta := ""
			if m.ReactionEmoji != "" {
				extraMeta += fmt.Sprintf(" [Reaction: %s]", m.ReactionEmoji)
			}
			if m.VoiceScript != "" {
				extraMeta += fmt.Sprintf(" [Voice Script: %q]", m.VoiceScript)
			}

			sb.WriteString(fmt.Sprintf("[%s] %s %s (User ID: %s%s)%s:\n%s\n\n",
				m.DateTime,
				roleTag,
				m.SenderDisplayName,
				m.UserID,
				replyInfo,
				extraMeta,
				m.MessageText,
			))
		}
		sb.WriteString("\n\n")
	}

	if _, err := txtFile.WriteString(sb.String()); err != nil {
		log.Fatalf("Failed to write to chat_history.txt: %v", err)
	}

	fmt.Printf("✅ chat_history.txt created: UTF-8 formatted (%d characters, %d lines)\n", sb.Len(), strings.Count(sb.String(), "\n"))
}
