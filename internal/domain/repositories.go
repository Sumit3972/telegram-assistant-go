package domain

import (
	"context"
)

type GroupRepository interface {
	GetGroup(ctx context.Context, chatID string) (*GroupSettings, error)
	CreateOrUpdateGroup(ctx context.Context, chatID, title string) error
	UpdateRules(ctx context.Context, chatID, rules string) error
	UpdateSettings(ctx context.Context, chatID string, antiSpam, captcha bool) error
	MigrateChatID(ctx context.Context, oldChatID, newChatID string) error
}

type AdminRepository interface {
	RegisterAdmin(ctx context.Context, chatID, userID, username, privateChatID string) error
	RegisterAdminPrivateChat(ctx context.Context, userID, privateChatID string) error
	GetAdmin(ctx context.Context, chatID, userID string) (*AdminInfo, error)
	GetAdminPrivateChatID(ctx context.Context, userID string) (string, error)
	GetGroupAdmins(ctx context.Context, chatID string) ([]AdminInfo, error)
}

type WarningRepository interface {
	AddWarning(ctx context.Context, chatID, userID, username, reason, warnedBy string) (int, error)
	GetWarnings(ctx context.Context, chatID, userID string) ([]WarningInfo, error)
	GetWarningCount(ctx context.Context, chatID, userID string) (int, error)
	ClearWarnings(ctx context.Context, chatID, userID string) error
}

type ModerationLogRepository interface {
	AddLog(ctx context.Context, chatID, userID, username, action, reason string, durationMinutes int, executedBy string) error
}

type HistoryRepository interface {
	AddMessage(ctx context.Context, chatID, userID, username, text, messageID, replyToID string) error
	GetRecentMessages(ctx context.Context, chatID string, limit int) ([]ChatMessageLog, error)
	ResolveUserIdentifier(ctx context.Context, chatID, identifier string) (userID, username string, err error)
	IsAdminActiveSince(ctx context.Context, chatID, adminID string, sinceTime int64) (bool, error)
}

type KarmaRepository interface {
	AddKarma(ctx context.Context, chatID, userID, username string, points int) (int, error)
	GetKarma(ctx context.Context, chatID, userID string) (int, error)
	GetTopKarma(ctx context.Context, chatID string, limit int) ([]KarmaInfo, error)
}

type CaptchaRepository interface {
	SetCaptchaSession(ctx context.Context, chatID, userID, username, correctOption, messageID string) error
	GetCaptchaSession(ctx context.Context, chatID, userID string) (*CaptchaSession, error)
	DeleteCaptchaSession(ctx context.Context, chatID, userID string) error
}

type MentionRepository interface {
	SetMentionSession(ctx context.Context, chatID, messageID, adminUserID, senderUserID string) error
	GetMentionSession(ctx context.Context, chatID, messageID string) (*MentionSession, error)
	DeleteMentionSession(ctx context.Context, chatID, messageID string) error
	RecordMentionLog(ctx context.Context, chatID, userID string) error
	GetRecentMentionCount(ctx context.Context, chatID, userID string, windowSeconds int64) (int, error)
}

type RelationshipRepository interface {
	GetRelationship(ctx context.Context, chatID, userID string) (*Relationship, error)
	UpdateAffection(ctx context.Context, chatID, userID, username string, delta int) (int, error)
}

type ShipRepository interface {
	GetDailyShip(ctx context.Context, chatID, date string) (*DailyShip, error)
	SaveDailyShip(ctx context.Context, ship *DailyShip) error
}

type ApiKeyRepository interface {
	InitSchemaAndSeed(ctx context.Context, defaultKeys []string, envKey string) error
	GetActiveKeys(ctx context.Context, provider string, fallbackKeys []string) ([]string, error)
	MarkExhausted(ctx context.Context, provider, key string) error
	ResetDailyExhausted(ctx context.Context) error
	RecordUsage(ctx context.Context, provider, key string) error
}

type PerformanceRepository interface {
	RecordPerformance(ctx context.Context, providerURL, modelName string, isSuccess bool, latencyMs int64) error
	GetModelScores(ctx context.Context) (map[string]float64, error)
}
