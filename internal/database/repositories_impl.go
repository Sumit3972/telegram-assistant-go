package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"telegram-ai-assistant/internal/domain"
)

// --- Group Repository ---

type groupRepo struct {
	pool *pgxpool.Pool
}

func NewGroupRepository(pool *pgxpool.Pool) domain.GroupRepository {
	return &groupRepo{pool: pool}
}

func (r *groupRepo) GetGroup(ctx context.Context, chatID string) (*domain.GroupSettings, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT chat_id, title, rules, anti_spam_enabled, welcome_message, captcha_enabled,
		       profanity_words, ban_warning_limit, mention_limit, delete_on_profanity, report_on_profanity, created_at
		FROM groups WHERE chat_id = $1
	`, chatID)

	var g domain.GroupSettings
	err := row.Scan(
		&g.ChatID, &g.Title, &g.Rules, &g.AntiSpamEnabled, &g.WelcomeMessage, &g.CaptchaEnabled,
		&g.ProfanityWords, &g.BanWarningLimit, &g.MentionLimit, &g.DeleteOnProfanity, &g.ReportOnProfanity, &g.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

func (r *groupRepo) CreateOrUpdateGroup(ctx context.Context, chatID, title string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO groups (chat_id, title)
		VALUES ($1, $2)
		ON CONFLICT (chat_id) DO UPDATE SET title = EXCLUDED.title
	`, chatID, title)
	return err
}

func (r *groupRepo) UpdateRules(ctx context.Context, chatID, rules string) error {
	_, err := r.pool.Exec(ctx, `UPDATE groups SET rules = $1 WHERE chat_id = $2`, rules, chatID)
	return err
}

func (r *groupRepo) UpdateSettings(ctx context.Context, chatID string, antiSpam, captcha bool) error {
	asInt := 0
	if antiSpam {
		asInt = 1
	}
	cInt := 0
	if captcha {
		cInt = 1
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE groups SET anti_spam_enabled = $1, captcha_enabled = $2 WHERE chat_id = $3
	`, asInt, cInt, chatID)
	return err
}

func (r *groupRepo) MigrateChatID(ctx context.Context, oldChatID, newChatID string) error {
	queries := []string{
		`UPDATE groups SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE admins SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE warnings SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE moderation_logs SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE chat_history SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE karma SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE captcha_sessions SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE mention_sessions SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE mention_logs SET chat_id = $2 WHERE chat_id = $1`,
		`UPDATE relationships SET chat_id = $2 WHERE chat_id = $1`,
	}
	for _, q := range queries {
		_, _ = r.pool.Exec(ctx, q, oldChatID, newChatID)
	}
	return nil
}

// --- Admin Repository ---

type adminRepo struct {
	pool *pgxpool.Pool
}

func NewAdminRepository(pool *pgxpool.Pool) domain.AdminRepository {
	return &adminRepo{pool: pool}
}

func (r *adminRepo) RegisterAdmin(ctx context.Context, chatID, userID, username, privateChatID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO admins (chat_id, user_id, username, private_chat_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (chat_id, user_id) DO UPDATE SET
			username = EXCLUDED.username,
			private_chat_id = EXCLUDED.private_chat_id
	`, chatID, userID, username, privateChatID)
	return err
}

func (r *adminRepo) RegisterAdminPrivateChat(ctx context.Context, userID, privateChatID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO admin_dms (user_id, private_chat_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET private_chat_id = EXCLUDED.private_chat_id
	`, userID, privateChatID)
	return err
}

func (r *adminRepo) GetAdmin(ctx context.Context, chatID, userID string) (*domain.AdminInfo, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT chat_id, user_id, username, private_chat_id
		FROM admins WHERE chat_id = $1 AND user_id = $2
	`, chatID, userID)

	var a domain.AdminInfo
	err := row.Scan(&a.ChatID, &a.UserID, &a.Username, &a.PrivateChatID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *adminRepo) GetAdminPrivateChatID(ctx context.Context, userID string) (string, error) {
	var privateChatID string
	err := r.pool.QueryRow(ctx, `SELECT private_chat_id FROM admin_dms WHERE user_id = $1 LIMIT 1`, userID).Scan(&privateChatID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return privateChatID, nil
}

func (r *adminRepo) GetGroupAdmins(ctx context.Context, chatID string) ([]domain.AdminInfo, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT chat_id, user_id, username, private_chat_id
		FROM admins WHERE chat_id = $1
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var admins []domain.AdminInfo
	for rows.Next() {
		var a domain.AdminInfo
		if err := rows.Scan(&a.ChatID, &a.UserID, &a.Username, &a.PrivateChatID); err == nil {
			admins = append(admins, a)
		}
	}
	return admins, nil
}

// --- Warning Repository ---

type warningRepo struct {
	pool *pgxpool.Pool
}

func NewWarningRepository(pool *pgxpool.Pool) domain.WarningRepository {
	return &warningRepo{pool: pool}
}

func (r *warningRepo) AddWarning(ctx context.Context, chatID, userID, username, reason, warnedBy string) (int, error) {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO warnings (chat_id, user_id, username, reason, warned_by)
		VALUES ($1, $2, $3, $4, $5)
	`, chatID, userID, username, reason, warnedBy)
	if err != nil {
		return 0, err
	}
	return r.GetWarningCount(ctx, chatID, userID)
}

func (r *warningRepo) GetWarnings(ctx context.Context, chatID, userID string) ([]domain.WarningInfo, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, chat_id, user_id, username, reason, warned_by, created_at
		FROM warnings WHERE chat_id = $1 AND user_id = $2
		ORDER BY created_at ASC
	`, chatID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var warns []domain.WarningInfo
	for rows.Next() {
		var w domain.WarningInfo
		if err := rows.Scan(&w.ID, &w.ChatID, &w.UserID, &w.Username, &w.Reason, &w.WarnedBy, &w.CreatedAt); err == nil {
			warns = append(warns, w)
		}
	}
	return warns, nil
}

func (r *warningRepo) GetWarningCount(ctx context.Context, chatID, userID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM warnings WHERE chat_id = $1 AND user_id = $2`, chatID, userID).Scan(&count)
	return count, err
}

func (r *warningRepo) ClearWarnings(ctx context.Context, chatID, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM warnings WHERE chat_id = $1 AND user_id = $2`, chatID, userID)
	return err
}

// --- Moderation Log Repository ---

type modLogRepo struct {
	pool *pgxpool.Pool
}

func NewModerationLogRepository(pool *pgxpool.Pool) domain.ModerationLogRepository {
	return &modLogRepo{pool: pool}
}

func (r *modLogRepo) AddLog(ctx context.Context, chatID, userID, username, action, reason string, durationMinutes int, executedBy string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO moderation_logs (chat_id, user_id, username, action, reason, duration_minutes, executed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, chatID, userID, username, action, reason, durationMinutes, executedBy)
	return err
}

// --- History Repository ---

type historyRepo struct {
	pool *pgxpool.Pool
}

func NewHistoryRepository(pool *pgxpool.Pool) domain.HistoryRepository {
	return &historyRepo{pool: pool}
}

func (r *historyRepo) AddMessage(ctx context.Context, chatID, userID, username, text, messageID, replyToID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO chat_history (chat_id, user_id, username, message_text, message_id, reply_to_message_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, chatID, userID, username, text, messageID, replyToID)
	return err
}

func (r *historyRepo) GetRecentMessages(ctx context.Context, chatID string, limit int) ([]domain.ChatMessageLog, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, chat_id, user_id, username, message_text, message_id, reply_to_message_id, created_at
		FROM chat_history
		WHERE chat_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.ChatMessageLog
	for rows.Next() {
		var m domain.ChatMessageLog
		if err := rows.Scan(&m.ID, &m.ChatID, &m.UserID, &m.Username, &m.MessageText, &m.MessageID, &m.ReplyToMessageID, &m.CreatedAt); err == nil {
			list = append(list, m)
		}
	}

	// Reverse to chronological order
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

func (r *historyRepo) ResolveUserIdentifier(ctx context.Context, chatID, identifier string) (string, string, error) {
	if identifier == "" {
		return "", "", nil
	}

	cleanUsername := strings.TrimPrefix(identifier, "@")

	// 1. Try from chat history
	var userID, username string
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, username FROM chat_history
		WHERE chat_id = $1 AND LOWER(username) = LOWER($2)
		ORDER BY created_at DESC LIMIT 1
	`, chatID, cleanUsername).Scan(&userID, &username)
	if err == nil {
		return userID, username, nil
	}

	// 2. Try from karma
	err = r.pool.QueryRow(ctx, `
		SELECT user_id, username FROM karma
		WHERE chat_id = $1 AND LOWER(username) = LOWER($2)
		LIMIT 1
	`, chatID, cleanUsername).Scan(&userID, &username)
	if err == nil {
		return userID, username, nil
	}

	// 3. Try from warnings
	err = r.pool.QueryRow(ctx, `
		SELECT user_id, username FROM warnings
		WHERE chat_id = $1 AND LOWER(username) = LOWER($2)
		LIMIT 1
	`, chatID, cleanUsername).Scan(&userID, &username)
	if err == nil {
		return userID, username, nil
	}

	return "", "", nil
}

func (r *historyRepo) IsAdminActiveSince(ctx context.Context, chatID, adminID string, sinceTime int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM chat_history
			WHERE chat_id = $1 AND user_id = $2 AND created_at >= $3
		)
	`, chatID, adminID, sinceTime).Scan(&exists)
	return exists, err
}

// --- Karma Repository ---

type karmaRepo struct {
	pool *pgxpool.Pool
}

func NewKarmaRepository(pool *pgxpool.Pool) domain.KarmaRepository {
	return &karmaRepo{pool: pool}
}

func (r *karmaRepo) AddKarma(ctx context.Context, chatID, userID, username string, points int) (int, error) {
	var newPoints int
	err := r.pool.QueryRow(ctx, `
		INSERT INTO karma (chat_id, user_id, username, points)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (chat_id, user_id) DO UPDATE SET
			points = karma.points + EXCLUDED.points,
			username = EXCLUDED.username
		RETURNING points
	`, chatID, userID, username, points).Scan(&newPoints)
	return newPoints, err
}

func (r *karmaRepo) GetKarma(ctx context.Context, chatID, userID string) (int, error) {
	var points int
	err := r.pool.QueryRow(ctx, `SELECT points FROM karma WHERE chat_id = $1 AND user_id = $2`, chatID, userID).Scan(&points)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return points, nil
}

func (r *karmaRepo) GetTopKarma(ctx context.Context, chatID string, limit int) ([]domain.KarmaInfo, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT chat_id, user_id, username, points
		FROM karma WHERE chat_id = $1
		ORDER BY points DESC LIMIT $2
	`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []domain.KarmaInfo
	for rows.Next() {
		var k domain.KarmaInfo
		if err := rows.Scan(&k.ChatID, &k.UserID, &k.Username, &k.Points); err == nil {
			list = append(list, k)
		}
	}
	return list, nil
}

// --- Captcha Repository ---

type captchaRepo struct {
	pool *pgxpool.Pool
}

func NewCaptchaRepository(pool *pgxpool.Pool) domain.CaptchaRepository {
	return &captchaRepo{pool: pool}
}

func (r *captchaRepo) SetCaptchaSession(ctx context.Context, chatID, userID, username, correctOption, messageID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO captcha_sessions (chat_id, user_id, username, correct_option, message_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (chat_id, user_id) DO UPDATE SET
			correct_option = EXCLUDED.correct_option,
			message_id = EXCLUDED.message_id,
			created_at = EXTRACT(EPOCH FROM NOW())::BIGINT
	`, chatID, userID, username, correctOption, messageID)
	return err
}

func (r *captchaRepo) GetCaptchaSession(ctx context.Context, chatID, userID string) (*domain.CaptchaSession, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT chat_id, user_id, username, correct_option, message_id, created_at
		FROM captcha_sessions WHERE chat_id = $1 AND user_id = $2
	`, chatID, userID)

	var s domain.CaptchaSession
	err := row.Scan(&s.ChatID, &s.UserID, &s.Username, &s.CorrectOption, &s.MessageID, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *captchaRepo) DeleteCaptchaSession(ctx context.Context, chatID, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM captcha_sessions WHERE chat_id = $1 AND user_id = $2`, chatID, userID)
	return err
}

// --- Mention Repository ---

type mentionRepo struct {
	pool *pgxpool.Pool
}

func NewMentionRepository(pool *pgxpool.Pool) domain.MentionRepository {
	return &mentionRepo{pool: pool}
}

func (r *mentionRepo) SetMentionSession(ctx context.Context, chatID, messageID, adminUserID, senderUserID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO mention_sessions (chat_id, message_id, admin_user_id, sender_user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (chat_id, message_id) DO UPDATE SET
			admin_user_id = EXCLUDED.admin_user_id,
			sender_user_id = EXCLUDED.sender_user_id,
			created_at = EXTRACT(EPOCH FROM NOW())::BIGINT
	`, chatID, messageID, adminUserID, senderUserID)
	return err
}

func (r *mentionRepo) GetMentionSession(ctx context.Context, chatID, messageID string) (*domain.MentionSession, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT chat_id, message_id, admin_user_id, sender_user_id, created_at
		FROM mention_sessions WHERE chat_id = $1 AND message_id = $2
	`, chatID, messageID)

	var s domain.MentionSession
	err := row.Scan(&s.ChatID, &s.MessageID, &s.AdminUserID, &s.SenderUserID, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *mentionRepo) DeleteMentionSession(ctx context.Context, chatID, messageID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM mention_sessions WHERE chat_id = $1 AND message_id = $2`, chatID, messageID)
	return err
}

func (r *mentionRepo) RecordMentionLog(ctx context.Context, chatID, userID string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO mention_logs (chat_id, user_id) VALUES ($1, $2)`, chatID, userID)
	return err
}

func (r *mentionRepo) GetRecentMentionCount(ctx context.Context, chatID, userID string, windowSeconds int64) (int, error) {
	cutoff := time.Now().Unix() - windowSeconds
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mention_logs
		WHERE chat_id = $1 AND user_id = $2 AND created_at >= $3
	`, chatID, userID, cutoff).Scan(&count)
	return count, err
}

// --- Relationship Repository ---

type relationshipRepo struct {
	pool *pgxpool.Pool
}

func NewRelationshipRepository(pool *pgxpool.Pool) domain.RelationshipRepository {
	return &relationshipRepo{pool: pool}
}

func (r *relationshipRepo) GetRelationship(ctx context.Context, chatID, userID string) (*domain.Relationship, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT chat_id, user_id, username, affection_score, last_interaction
		FROM relationships WHERE chat_id = $1 AND user_id = $2
	`, chatID, userID)

	var rel domain.Relationship
	err := row.Scan(&rel.ChatID, &rel.UserID, &rel.Username, &rel.AffectionScore, &rel.LastInteraction)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

func (r *relationshipRepo) UpdateAffection(ctx context.Context, chatID, userID, username string, delta int) (int, error) {
	var currentScore int = 50
	existing, err := r.GetRelationship(ctx, chatID, userID)
	if err == nil && existing != nil {
		currentScore = existing.AffectionScore
	}

	newScore := currentScore + delta
	if newScore < 0 {
		newScore = 0
	} else if newScore > 100 {
		newScore = 100
	}

	now := time.Now().Unix()
	_, err = r.pool.Exec(ctx, `
		INSERT INTO relationships (chat_id, user_id, username, affection_score, last_interaction)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (chat_id, user_id) DO UPDATE SET
			affection_score = EXCLUDED.affection_score,
			username = EXCLUDED.username,
			last_interaction = EXCLUDED.last_interaction
	`, chatID, userID, username, newScore, now)

	return newScore, err
}

// --- Daily Ship Repository ---

type shipRepo struct {
	pool *pgxpool.Pool
}

func NewShipRepository(pool *pgxpool.Pool) domain.ShipRepository {
	return &shipRepo{pool: pool}
}

func (r *shipRepo) GetDailyShip(ctx context.Context, chatID, date string) (*domain.DailyShip, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT chat_id, user1_id, user1_name, user2_id, user2_name, ship_date
		FROM daily_ships WHERE chat_id = $1 AND ship_date = $2
	`, chatID, date)

	var s domain.DailyShip
	err := row.Scan(&s.ChatID, &s.User1ID, &s.User1Name, &s.User2ID, &s.User2Name, &s.ShipDate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func (r *shipRepo) SaveDailyShip(ctx context.Context, ship *domain.DailyShip) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO daily_ships (chat_id, user1_id, user1_name, user2_id, user2_name, ship_date)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (chat_id) DO UPDATE SET
			user1_id = EXCLUDED.user1_id,
			user1_name = EXCLUDED.user1_name,
			user2_id = EXCLUDED.user2_id,
			user2_name = EXCLUDED.user2_name,
			ship_date = EXCLUDED.ship_date
	`, ship.ChatID, ship.User1ID, ship.User1Name, ship.User2ID, ship.User2Name, ship.ShipDate)
	return err
}

// --- API Key Repository ---

type apiKeyRepo struct {
	pool *pgxpool.Pool
}

func NewApiKeyRepository(pool *pgxpool.Pool) domain.ApiKeyRepository {
	return &apiKeyRepo{pool: pool}
}

func (r *apiKeyRepo) InitSchemaAndSeed(ctx context.Context, defaultKeys []string, envKey string) error {
	if err := r.ResetDailyExhausted(ctx); err != nil {
		log.Printf("[ApiKeyRepo] Daily reset warning: %v", err)
	}

	keysToSeed := make(map[string]struct{})
	for _, k := range defaultKeys {
		if strings.TrimSpace(k) != "" {
			keysToSeed[strings.TrimSpace(k)] = struct{}{}
		}
	}
	if strings.TrimSpace(envKey) != "" {
		keysToSeed[strings.TrimSpace(envKey)] = struct{}{}
	}

	for k := range keysToSeed {
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO api_keys (provider, api_key, status)
			VALUES ('anyapi', $1, 'active')
			ON CONFLICT (provider, api_key) DO NOTHING
		`, k)
	}

	log.Println("✅ ApiKeyRepository initialized and keys seeded.")
	return nil
}

func (r *apiKeyRepo) ResetDailyExhausted(ctx context.Context) error {
	res, err := r.pool.Exec(ctx, `
		UPDATE api_keys
		SET status = 'active', exhausted_at = NULL
		WHERE status = 'exhausted'
		  AND (exhausted_at::date < CURRENT_DATE OR exhausted_at IS NULL);
	`)
	if err == nil && res.RowsAffected() > 0 {
		log.Printf("[ApiKeyRepo] Reset %d exhausted key(s) for a new day.", res.RowsAffected())
	}
	return err
}

func (r *apiKeyRepo) GetActiveKeys(ctx context.Context, provider string, fallbackKeys []string) ([]string, error) {
	_ = r.ResetDailyExhausted(ctx)

	rows, err := r.pool.Query(ctx, `
		SELECT api_key FROM api_keys
		WHERE provider = $1 AND status = 'active'
		ORDER BY id ASC
	`, provider)
	if err != nil {
		return fallbackKeys, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return fallbackKeys, nil
	}
	return keys, nil
}

func (r *apiKeyRepo) MarkExhausted(ctx context.Context, provider, key string) error {
	log.Printf("[ApiKeyRepo] Marking API key %s... as EXHAUSTED for provider '%s'", key[:min(len(key), 12)], provider)
	_, err := r.pool.Exec(ctx, `
		UPDATE api_keys
		SET status = 'exhausted', exhausted_at = CURRENT_TIMESTAMP
		WHERE provider = $1 AND api_key = $2
	`, provider, key)
	return err
}

func (r *apiKeyRepo) RecordUsage(ctx context.Context, provider, key string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE api_keys
		SET last_used_at = CURRENT_TIMESTAMP
		WHERE provider = $1 AND api_key = $2
	`, provider, key)
	return err
}

// --- Performance Repository ---

type perfRepo struct {
	pool *pgxpool.Pool
}

func NewPerformanceRepository(pool *pgxpool.Pool) domain.PerformanceRepository {
	return &perfRepo{pool: pool}
}

func (r *perfRepo) RecordPerformance(ctx context.Context, providerURL, modelName string, isSuccess bool, latencyMs int64) error {
	successInt := 0
	if isSuccess {
		successInt = 1
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO model_performance (provider_url, model_name, total_requests, successful_requests, total_latency_ms, avg_latency_ms)
		VALUES ($1, $2, 1, $3, $4::BIGINT, $4::INTEGER)
		ON CONFLICT (provider_url, model_name) DO UPDATE SET
			total_requests = model_performance.total_requests + 1,
			successful_requests = model_performance.successful_requests + EXCLUDED.successful_requests,
			total_latency_ms = model_performance.total_latency_ms + EXCLUDED.total_latency_ms,
			avg_latency_ms = (model_performance.total_latency_ms + EXCLUDED.total_latency_ms) / (model_performance.total_requests + 1)
	`, providerURL, modelName, successInt, latencyMs)
	return err
}

func (r *perfRepo) GetModelScores(ctx context.Context) (map[string]float64, error) {
	scores := make(map[string]float64)
	rows, err := r.pool.Query(ctx, `
		SELECT provider_url, model_name, successful_requests, total_requests, avg_latency_ms
		FROM model_performance
	`)
	if err != nil {
		return scores, err
	}
	defer rows.Close()

	for rows.Next() {
		var pURL, mName string
		var succ, total, avgLat int
		if err := rows.Scan(&pURL, &mName, &succ, &total, &avgLat); err == nil {
			key := fmt.Sprintf("%s|%s", pURL, mName)
			succRate := 1.0
			if total > 0 {
				succRate = float64(succ) / float64(total)
			}
			avgSec := float64(avgLat) / 1000.0
			scores[key] = succRate / (avgSec + 0.1)
		}
	}
	return scores, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
