package moderator

import (
	"context"
	"testing"
	"time"

	"telegram-ai-assistant/internal/decision"
	"telegram-ai-assistant/internal/domain"
)

func TestEvaluateAbuseFromDecision(t *testing.T) {
	tests := []struct {
		pre            decision.PreDecisionResult
		expectedAbuse  bool
		expectedSevere bool
		expectedMax    int
		desc           string
	}{
		{
			pre: decision.PreDecisionResult{
				AbuseSeverity: "severe_threat",
				IsAbuse:       true,
				MaxStrikes:    3,
			},
			expectedAbuse:  true,
			expectedSevere: true,
			expectedMax:    3,
			desc:           "Severe physical assault threat",
		},
		{
			pre: decision.PreDecisionResult{
				AbuseSeverity: "hostile_abuse",
				IsAbuse:       true,
				MaxStrikes:    4,
			},
			expectedAbuse:  true,
			expectedSevere: false,
			expectedMax:    4,
			desc:           "Hostile slurs and abusive attack",
		},
		{
			pre: decision.PreDecisionResult{
				AbuseSeverity: "annoyance",
				IsAbuse:       true,
				MaxStrikes:    5,
			},
			expectedAbuse:  true,
			expectedSevere: false,
			expectedMax:    5,
			desc:           "Moderate harassment / nuisance",
		},
		{
			pre: decision.PreDecisionResult{
				AbuseSeverity: "none",
				IsAbuse:       false,
				MaxStrikes:    0,
			},
			expectedAbuse:  false,
			expectedSevere: false,
			expectedMax:    0,
			desc:           "Consensual romance or 18+ photo request",
		},
	}

	for _, tc := range tests {
		abuse, severe, maxStrikes, _ := EvaluateAbuseFromDecision(tc.pre)
		if abuse != tc.expectedAbuse || severe != tc.expectedSevere || maxStrikes != tc.expectedMax {
			t.Errorf("[%s] EvaluateAbuseFromDecision() = (abuse:%v, severe:%v, max:%d), expected (abuse:%v, severe:%v, max:%d)",
				tc.desc, abuse, severe, maxStrikes, tc.expectedAbuse, tc.expectedSevere, tc.expectedMax)
		}
	}
}

type mockAbuseRepo struct {
	records []domain.AbuseRecord
}

func (m *mockAbuseRepo) RecordAbuse(ctx context.Context, record *domain.AbuseRecord) error {
	m.records = append(m.records, *record)
	return nil
}

func (m *mockAbuseRepo) GetActiveAbuseStrikes(ctx context.Context, chatID, userID string, windowDuration time.Duration) (int, error) {
	cutoff := time.Now().Add(-windowDuration).Unix()
	count := 0
	for _, r := range m.records {
		if r.ChatID == chatID && r.UserID == userID {
			if r.AbuseType == "severe_threat" || r.CreatedAt >= cutoff {
				count++
			}
		}
	}
	return count, nil
}

func (m *mockAbuseRepo) GetAbuseHistory(ctx context.Context, chatID, userID string, limit int) ([]domain.AbuseRecord, error) {
	return m.records, nil
}

func (m *mockAbuseRepo) ClearAbuseStrikes(ctx context.Context, userID string) error {
	var filtered []domain.AbuseRecord
	for _, r := range m.records {
		if r.UserID != userID {
			filtered = append(filtered, r)
		}
	}
	m.records = filtered
	return nil
}

type mockBlockedRepo struct {
	blocked map[string]domain.BlockedUser
}

func (m *mockBlockedRepo) BlockUser(ctx context.Context, chatID, userID, username, firstName, lastName, reason, blockedBy string, strikeCount int) error {
	m.blocked[chatID+":"+userID] = domain.BlockedUser{
		ChatID: chatID, UserID: userID, Username: username, FirstName: firstName, LastName: lastName, Reason: reason, BlockedBy: blockedBy, StrikeCount: strikeCount,
	}
	return nil
}

func (m *mockBlockedRepo) IsUserBlocked(ctx context.Context, chatID, userID string) (bool, error) {
	_, ok := m.blocked[chatID+":"+userID]
	return ok, nil
}

func (m *mockBlockedRepo) UnblockUser(ctx context.Context, chatID, userID string) error {
	delete(m.blocked, chatID+":"+userID)
	return nil
}

func (m *mockBlockedRepo) GetBlockedUsers(ctx context.Context) ([]domain.BlockedUser, error) {
	var list []domain.BlockedUser
	for _, u := range m.blocked {
		list = append(list, u)
	}
	return list, nil
}

func TestAbuseAndBlockedTables(t *testing.T) {
	now := time.Now().Unix()
	twoDaysAgo := now - (48 * 3600)
	oneHourAgo := now - 3600

	abuseRepo := &mockAbuseRepo{
		records: []domain.AbuseRecord{
			{ChatID: "123", UserID: "456", AbuseType: "annoyance", CreatedAt: twoDaysAgo},       // Decayed
			{ChatID: "123", UserID: "456", AbuseType: "hostile_abuse", CreatedAt: oneHourAgo},   // Active
			{ChatID: "123", UserID: "456", AbuseType: "severe_threat", CreatedAt: twoDaysAgo},  // Never decays
		},
	}

	active, err := GetActiveStrikesWithDecay(context.Background(), abuseRepo, "123", "456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active != 2 {
		t.Errorf("GetActiveStrikesWithDecay() = %d, expected 2", active)
	}

	blockedRepo := &mockBlockedRepo{blocked: make(map[string]domain.BlockedUser)}
	isBlocked, _ := CheckIfUserBlocked(context.Background(), blockedRepo, "123", "456")
	if isBlocked {
		t.Errorf("expected user not to be blocked initially")
	}

	_ = RecordBlockedUser(context.Background(), blockedRepo, "123", "456", "goku", "Goku", "", "threats", "chavi_ai", 3)
	isBlocked, _ = CheckIfUserBlocked(context.Background(), blockedRepo, "123", "456")
	if !isBlocked {
		t.Errorf("expected user to be blocked after RecordBlockedUser")
	}
}
