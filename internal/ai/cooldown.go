package ai

import (
	"fmt"
	"sync"
	"time"
)

type CooldownManager struct {
	mu        sync.RWMutex
	cooldowns map[string]time.Time
}

func NewCooldownManager() *CooldownManager {
	return &CooldownManager{
		cooldowns: make(map[string]time.Time),
	}
}

func (cm *CooldownManager) PutOnCooldown(baseURL, model string, duration time.Duration, apiKey ...string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key := fmt.Sprintf("%s|%s", baseURL, model)
	if len(apiKey) > 0 && apiKey[0] != "" {
		key = fmt.Sprintf("%s|%s|%s", baseURL, model, apiKey[0])
	}
	cm.cooldowns[key] = time.Now().Add(duration)
}

func (cm *CooldownManager) IsOnCooldown(baseURL, model string, apiKey ...string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	now := time.Now()
	// Check key with apiKey
	if len(apiKey) > 0 && apiKey[0] != "" {
		keyWithKey := fmt.Sprintf("%s|%s|%s", baseURL, model, apiKey[0])
		if until, ok := cm.cooldowns[keyWithKey]; ok && now.Before(until) {
			return true
		}
	}

	// Check key without apiKey
	keyWithoutKey := fmt.Sprintf("%s|%s", baseURL, model)
	if until, ok := cm.cooldowns[keyWithoutKey]; ok && now.Before(until) {
		return true
	}

	return false
}
