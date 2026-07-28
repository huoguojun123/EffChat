package handler

import (
	"fmt"
	"testing"
	"time"

	"github.com/huoguojun123/effchat/pkg/config"
)

func TestAuthRateLimiterBlocksAndResets(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		Block:       5 * time.Minute,
	})
	limiter.now = func() time.Time { return now }

	if _, ok := limiter.Allow("1.2.3.4", "Admin"); !ok {
		t.Fatal("first attempt should be allowed")
	}
	limiter.RecordFailure("1.2.3.4", "admin")
	limiter.RecordFailure("1.2.3.4", "admin")
	if retry, ok := limiter.Allow("1.2.3.4", "ADMIN"); ok || retry <= 0 {
		t.Fatalf("blocked attempt = ok:%t retry:%v, want blocked with retry", ok, retry)
	}

	limiter.Reset("1.2.3.4", "admin")
	if _, ok := limiter.Allow("1.2.3.4", "admin"); !ok {
		t.Fatal("reset should allow future attempts")
	}
}

func TestAuthRateLimiterWindowExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		Block:       5 * time.Minute,
	})
	limiter.now = func() time.Time { return now }

	limiter.RecordFailure("1.2.3.4", "admin")
	now = now.Add(2 * time.Minute)
	limiter.RecordFailure("1.2.3.4", "admin")
	if _, ok := limiter.Allow("1.2.3.4", "admin"); !ok {
		t.Fatal("old failure should expire before it can block a new window")
	}
}

func TestAuthRateLimiterCleansExpiredKeys(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		Block:       time.Minute,
	})
	limiter.now = func() time.Time { return now }

	limiter.RecordFailure("1.2.3.4", "admin")
	limiter.RecordFailure("5.6.7.8", "user")
	if got := len(limiter.attempts); got != 2 {
		t.Fatalf("attempts len = %d, want 2", got)
	}

	now = now.Add(3 * time.Minute)
	limiter.RecordFailure("9.9.9.9", "new")
	if got := len(limiter.attempts); got != 1 {
		t.Fatalf("attempts len after cleanup = %d, want 1", got)
	}
	if _, ok := limiter.attempts[authRateLimitKey("9.9.9.9", "new")]; !ok {
		t.Fatal("new attempt should remain after cleanup")
	}
}

func TestAuthRateLimiterAllowCleansExpiredKeys(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		Block:       time.Minute,
	})
	limiter.now = func() time.Time { return now }

	limiter.RecordFailure("1.2.3.4", "admin")
	now = now.Add(3 * time.Minute)
	if _, ok := limiter.Allow("9.9.9.9", "new"); !ok {
		t.Fatal("new key should be allowed")
	}
	if got := len(limiter.attempts); got != 0 {
		t.Fatalf("attempts len after Allow cleanup = %d, want 0", got)
	}
}

func TestAuthRateLimiterAppliesLimitAcrossAccountsFromSameSource(t *testing.T) {
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		Block:       time.Minute,
	})
	limiter.RecordFailure("1.2.3.4", "first-account")
	limiter.RecordFailure("1.2.3.4", "second-account")
	if _, ok := limiter.Allow("1.2.3.4", "third-account"); ok {
		t.Fatal("same source should be blocked across account names")
	}
}

func TestAuthRateLimiterResetAccountClearsTrackedSource(t *testing.T) {
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 3,
		Window:      time.Minute,
		Block:       time.Minute,
	})
	limiter.RecordFailure("1.2.3.4", "pending-user")
	limiter.RecordFailure("1.2.3.4", "pending-user")
	limiter.RecordFailure("1.2.3.4", "other-user")
	if _, ok := limiter.Allow("1.2.3.4", "pending-user"); ok {
		t.Fatal("source should be blocked before an administrator re-enables the account")
	}

	limiter.ResetAccount("PENDING-USER")
	if _, ok := limiter.Allow("1.2.3.4", "pending-user"); !ok {
		t.Fatal("re-enabled account should not remain blocked by previous source failures")
	}

	limiter.RecordFailure("1.2.3.4", "other-user")
	limiter.RecordFailure("1.2.3.4", "other-user")
	if _, ok := limiter.Allow("1.2.3.4", "other-user"); ok {
		t.Fatal("re-enabling one account must retain failures from other accounts on the same source")
	}
}

func TestAuthRateLimiterSuccessfulLoginRetainsOtherAccountFailures(t *testing.T) {
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 3,
		Window:      time.Minute,
		Block:       time.Minute,
	})
	limiter.RecordFailure("1.2.3.4", "successful-user")
	limiter.RecordFailure("1.2.3.4", "other-user")
	limiter.RecordFailure("1.2.3.4", "other-user")
	if _, ok := limiter.Allow("1.2.3.4", "any-user"); ok {
		t.Fatal("source should be blocked after failures from both accounts")
	}

	limiter.Reset("1.2.3.4", "successful-user")
	if _, ok := limiter.Allow("1.2.3.4", "any-user"); !ok {
		t.Fatal("clearing one account's contribution should unblock the source below the threshold")
	}
	limiter.RecordFailure("1.2.3.4", "other-user")
	if _, ok := limiter.Allow("1.2.3.4", "any-user"); ok {
		t.Fatal("a successful login must retain failures from other accounts on the same source")
	}
}

func TestAuthRateLimiterBoundsTrackedSources(t *testing.T) {
	limiter := NewAuthRateLimiter(config.AuthRateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Hour,
		Block:       time.Hour,
	})
	for i := 0; i < maxAuthRateLimitEntries+32; i++ {
		limiter.RecordFailure(fmt.Sprintf("192.0.2.%d", i), "random-account")
	}
	if got := len(limiter.attempts); got > maxAuthRateLimitEntries {
		t.Fatalf("tracked sources = %d, want <= %d", got, maxAuthRateLimitEntries)
	}
}
