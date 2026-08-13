package handler

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/huoguojun123/EffChat/pkg/config"
)

const (
	maxAuthRateLimitEntries      = 2048
	authRateLimitCleanupInterval = time.Minute
	authRateLimitOverflowKey     = "__overflow__"
)

type AuthRateLimiter struct {
	mu          sync.Mutex
	maxAttempts int
	window      time.Duration
	block       time.Duration
	now         func() time.Time
	attempts    map[string]authAttemptState
	lastCleanup time.Time
}

type authAttemptState struct {
	failures        int
	windowStart     time.Time
	blockedUntil    time.Time
	accountFailures map[string]int
}

func NewAuthRateLimiter(cfg config.AuthRateLimitConfig) *AuthRateLimiter {
	if cfg.MaxAttempts <= 0 || cfg.Window <= 0 || cfg.Block <= 0 {
		return nil
	}
	return &AuthRateLimiter{
		maxAttempts: cfg.MaxAttempts,
		window:      cfg.Window,
		block:       cfg.Block,
		now:         time.Now,
		attempts:    make(map[string]authAttemptState),
	}
}

func (l *AuthRateLimiter) Allow(ip, account string) (time.Duration, bool) {
	if l == nil {
		return 0, true
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupExpiredLocked(now, false)
	key := l.trackedKeyLocked(ip, account)
	state := l.attempts[key]
	if !state.blockedUntil.IsZero() && now.Before(state.blockedUntil) {
		return state.blockedUntil.Sub(now), false
	}
	if state.windowStart.IsZero() || now.Sub(state.windowStart) > l.window {
		delete(l.attempts, key)
	}
	return 0, true
}

func (l *AuthRateLimiter) RecordFailure(ip, account string) {
	if l == nil {
		return
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupExpiredLocked(now, len(l.attempts) >= maxAuthRateLimitEntries-1)
	key := l.trackedKeyLocked(ip, account)
	state := l.attempts[key]
	if state.windowStart.IsZero() || now.Sub(state.windowStart) > l.window {
		state = authAttemptState{windowStart: now}
	}
	if state.accountFailures == nil {
		state.accountFailures = make(map[string]int)
	}
	if account = strings.ToLower(strings.TrimSpace(account)); account != "" {
		state.accountFailures[account]++
	}
	state.failures++
	if state.failures >= l.maxAttempts {
		state.blockedUntil = now.Add(l.block)
	}
	l.attempts[key] = state
}

func (l *AuthRateLimiter) Reset(ip, account string) {
	if l == nil {
		return
	}
	account = strings.ToLower(strings.TrimSpace(account))
	if account == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.removeAccountFailuresLocked(authRateLimitKey(ip, account), account)
}

func (l *AuthRateLimiter) ResetAccount(account string) {
	if l == nil {
		return
	}
	account = strings.ToLower(strings.TrimSpace(account))
	if account == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.attempts {
		l.removeAccountFailuresLocked(key, account)
	}
}

func (l *AuthRateLimiter) removeAccountFailuresLocked(key, account string) {
	state, ok := l.attempts[key]
	if !ok {
		return
	}
	failures := state.accountFailures[account]
	if failures == 0 {
		return
	}
	state.failures -= failures
	delete(state.accountFailures, account)
	if state.failures < l.maxAttempts {
		state.blockedUntil = time.Time{}
	}
	if state.failures <= 0 {
		delete(l.attempts, key)
		return
	}
	l.attempts[key] = state
}

func authRateLimitKey(ip, account string) string {
	_ = account
	source := strings.TrimSpace(ip)
	if source == "" {
		return "unknown"
	}
	return source
}

func (l *AuthRateLimiter) trackedKeyLocked(ip, account string) string {
	key := authRateLimitKey(ip, account)
	if _, exists := l.attempts[key]; exists || len(l.attempts) < maxAuthRateLimitEntries-1 {
		return key
	}
	return authRateLimitOverflowKey
}

func (l *AuthRateLimiter) cleanupExpiredLocked(now time.Time, force bool) {
	if !force && !l.lastCleanup.IsZero() && now.Sub(l.lastCleanup) < authRateLimitCleanupInterval {
		return
	}
	for key, state := range l.attempts {
		windowExpired := state.windowStart.IsZero() || now.Sub(state.windowStart) > l.window
		blockExpired := state.blockedUntil.IsZero() || !now.Before(state.blockedUntil)
		if windowExpired && blockExpired {
			delete(l.attempts, key)
		}
	}
	l.lastCleanup = now
}

func retryAfterSeconds(d time.Duration) string {
	if d <= 0 {
		return "1"
	}
	return fmt.Sprintf("%d", int(math.Ceil(d.Seconds())))
}
