package database

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AutoMigrate creates all database tables and indexes if they do not exist.
func AutoMigrate(ctx context.Context, pool *pgxpool.Pool) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS groups (
			chat_id TEXT PRIMARY KEY,
			title TEXT,
			rules TEXT DEFAULT '',
			anti_spam_enabled INTEGER DEFAULT 1,
			welcome_message TEXT DEFAULT 'Welcome to the group!',
			captcha_enabled INTEGER DEFAULT 1,
			profanity_words TEXT DEFAULT NULL,
			ban_warning_limit INTEGER DEFAULT 3,
			mention_limit INTEGER DEFAULT 5,
			delete_on_profanity INTEGER DEFAULT 1,
			report_on_profanity INTEGER DEFAULT 1,
			created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS admins (
			chat_id TEXT,
			user_id TEXT,
			username TEXT,
			private_chat_id TEXT,
			PRIMARY KEY (chat_id, user_id)
		);`,
		`CREATE TABLE IF NOT EXISTS admin_dms (
			user_id TEXT PRIMARY KEY,
			private_chat_id TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS warnings (
			id BIGSERIAL PRIMARY KEY,
			chat_id TEXT,
			user_id TEXT,
			username TEXT,
			reason TEXT,
			warned_by TEXT,
			created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS moderation_logs (
			id BIGSERIAL PRIMARY KEY,
			chat_id TEXT,
			user_id TEXT,
			username TEXT,
			action TEXT,
			reason TEXT,
			duration_minutes INTEGER,
			executed_by TEXT,
			created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS chat_history (
			id BIGSERIAL PRIMARY KEY,
			chat_id TEXT,
			user_id TEXT,
			username TEXT,
			message_text TEXT,
			message_id TEXT,
			reply_to_message_id TEXT,
			created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
		);`,
		`CREATE TABLE IF NOT EXISTS karma (
			chat_id TEXT,
			user_id TEXT,
			username TEXT,
			points INTEGER DEFAULT 0,
			PRIMARY KEY (chat_id, user_id)
		);`,
		`CREATE TABLE IF NOT EXISTS captcha_sessions (
			chat_id TEXT,
			user_id TEXT,
			username TEXT,
			correct_option TEXT,
			message_id TEXT,
			created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
			PRIMARY KEY (chat_id, user_id)
		);`,
		`CREATE TABLE IF NOT EXISTS mention_sessions (
			chat_id TEXT,
			message_id TEXT,
			admin_user_id TEXT,
			sender_user_id TEXT,
			created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
			PRIMARY KEY (chat_id, message_id)
		);`,
		`CREATE TABLE IF NOT EXISTS mention_logs (
			id BIGSERIAL PRIMARY KEY,
			chat_id TEXT,
			user_id TEXT,
			created_at BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mention_logs_user_time ON mention_logs (chat_id, user_id, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_chat_history_chat_time ON chat_history (chat_id, created_at);`,
		`CREATE TABLE IF NOT EXISTS relationships (
			chat_id TEXT,
			user_id TEXT,
			username TEXT,
			affection_score INTEGER DEFAULT 50,
			last_interaction BIGINT DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT,
			PRIMARY KEY (chat_id, user_id)
		);`,
		`CREATE TABLE IF NOT EXISTS daily_ships (
			chat_id TEXT PRIMARY KEY,
			user1_id TEXT,
			user1_name TEXT,
			user2_id TEXT,
			user2_name TEXT,
			ship_date TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS model_performance (
			provider_url TEXT,
			model_name TEXT,
			total_requests INTEGER DEFAULT 0,
			successful_requests INTEGER DEFAULT 0,
			total_latency_ms BIGINT DEFAULT 0,
			avg_latency_ms INTEGER DEFAULT 0,
			PRIMARY KEY (provider_url, model_name)
		);`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id SERIAL PRIMARY KEY,
			provider TEXT NOT NULL,
			api_key TEXT NOT NULL,
			status TEXT DEFAULT 'active',
			exhausted_at TIMESTAMP WITH TIME ZONE,
			last_used_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(provider, api_key)
		);`,
	}

	for _, query := range queries {
		if _, err := pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("migration query failed: %s : %w", query, err)
		}
	}

	log.Println("✅ PostgreSQL schema verified & auto-migrated successfully.")
	return nil
}
