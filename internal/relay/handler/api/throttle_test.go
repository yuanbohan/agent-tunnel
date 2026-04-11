package api

import (
	"testing"
	"time"
)

func TestRegisterThrottleLimitsFailuresPerIP(t *testing.T) {
	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	throttle := NewRegisterThrottle(2, 10*time.Minute)
	throttle.SetNowFunc(func() time.Time { return now })

	if allowed, retryAfter := throttle.Allow("203.0.113.10"); !allowed || retryAfter != 0 {
		t.Fatalf("Allow before failures = (%v, %v), want (true, 0)", allowed, retryAfter)
	}

	throttle.RecordFailure("203.0.113.10")
	if allowed, retryAfter := throttle.Allow("203.0.113.10"); !allowed || retryAfter != 0 {
		t.Fatalf("Allow after first failure = (%v, %v), want (true, 0)", allowed, retryAfter)
	}

	throttle.RecordFailure("203.0.113.10")
	allowed, retryAfter := throttle.Allow("203.0.113.10")
	if allowed {
		t.Fatal("Allow after threshold returned true, want false")
	}
	if retryAfter <= 0 || retryAfter > 10*time.Minute {
		t.Fatalf("retryAfter = %v, want within (0, 10m]", retryAfter)
	}

	if allowedOther, _ := throttle.Allow("203.0.113.11"); !allowedOther {
		t.Fatal("separate IP was throttled, want allowed")
	}
	if got := len(throttle.clientsByIP); got != 1 {
		t.Fatalf("len(clientsByIP) = %d, want 1", got)
	}
}

func TestRegisterThrottleResetsAfterWindowOrSuccess(t *testing.T) {
	now := time.Date(2026, time.April, 11, 12, 0, 0, 0, time.UTC)
	throttle := NewRegisterThrottle(1, 10*time.Minute)
	throttle.SetNowFunc(func() time.Time { return now })

	throttle.RecordFailure("203.0.113.10")
	if allowed, _ := throttle.Allow("203.0.113.10"); allowed {
		t.Fatal("Allow after one failure at limit returned true, want false")
	}

	throttle.Reset("203.0.113.10")
	if allowed, _ := throttle.Allow("203.0.113.10"); !allowed {
		t.Fatal("Allow after Reset returned false, want true")
	}

	throttle.RecordFailure("203.0.113.10")
	now = now.Add(11 * time.Minute)
	if allowed, retryAfter := throttle.Allow("203.0.113.10"); !allowed || retryAfter != 0 {
		t.Fatalf("Allow after window expiry = (%v, %v), want (true, 0)", allowed, retryAfter)
	}
}
