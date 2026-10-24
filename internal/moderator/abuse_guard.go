package moderator

import (
	"context"
	"time"

	"telegram-ai-assistant/internal/decision"
	"telegram-ai-assistant/internal/domain"
)

// EvaluateAbuseFromDecision categorizes an incoming message strictly using OpenJev System One LLM decision.
// Zero hardcoded regexes or keyword lists: all nuance is classified dynamically by OpenJev.
// Returns:
// 1. isAbuse: Whether this message represents actual hostile abuse or physical threat.
// 2. isSevereThreat: Whether this contains physical violence/stalking ("chhod ne aaunga", "ghar aaunga").
// 3. maxStrikes: Dynamic warning threshold (3 for severe threats, 4 for hostile slurs, 5 for annoyance).
// 4. reason: Human-readable rationale for moderation logs.
// Consensual romance, flirting, sexy desires, and 18+ photo requests always return isAbuse = false (0 strikes).
func EvaluateAbuseFromDecision(pre decision.PreDecisionResult) (isAbuse bool, isSevereThreat bool, maxStrikes int, reason string) {
	if !pre.IsAbuse || pre.AbuseSeverity == "none" || pre.AbuseSeverity == "" {
		return false, false, 0, ""
	}

	switch pre.AbuseSeverity {
	case "severe_threat":
		return true, true, 3, "physical/sexual violence threat or stalking"
	case "hostile_abuse":
		return true, false, 4, "hostile slurs and verbal abuse"
	case "annoyance":
		return true, false, 5, "persistent offensive harassment"
	default:
		return false, false, 0, ""
	}
}

// CheckIfUserBlocked checks the database blocked_users table to see if the user is already blocked.
func CheckIfUserBlocked(ctx context.Context, blockedRepo domain.BlockedUserRepository, chatID, userID string) (bool, error) {
	if blockedRepo == nil {
		return false, nil
	}
	return blockedRepo.IsUserBlocked(ctx, chatID, userID)
}

// GetActiveStrikesWithDecay calculates active strikes from the abuse_records table with a 24-hour decay
// for ordinary abuse. Severe physical threats never decay.
func GetActiveStrikesWithDecay(ctx context.Context, abuseRepo domain.AbuseRecordRepository, chatID, userID string) (int, error) {
	if abuseRepo == nil {
		return 0, nil
	}
	return abuseRepo.GetActiveAbuseStrikes(ctx, chatID, userID, 24*time.Hour)
}

// RecordAbuseStrike persists an abuse incident into the abuse_records database table.
func RecordAbuseStrike(ctx context.Context, abuseRepo domain.AbuseRecordRepository, record *domain.AbuseRecord) error {
	if abuseRepo == nil || record == nil {
		return nil
	}
	return abuseRepo.RecordAbuse(ctx, record)
}

// RecordBlockedUser persists a permanently blocked user into the blocked_users database table.
func RecordBlockedUser(ctx context.Context, blockedRepo domain.BlockedUserRepository, chatID, userID, username, firstName, lastName, reason, blockedBy string, strikeCount int) error {
	if blockedRepo == nil {
		return nil
	}
	return blockedRepo.BlockUser(ctx, chatID, userID, username, firstName, lastName, reason, blockedBy, strikeCount)
}
