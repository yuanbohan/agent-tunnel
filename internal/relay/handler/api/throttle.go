package api

import (
	"sync"
	"time"
)

type RegisterThrottle struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	now         func() time.Time
	clientsByIP map[string]throttleWindow
}

type throttleWindow struct {
	start    time.Time
	failures int
}

func NewRegisterThrottle(limit int, window time.Duration) *RegisterThrottle {
	if limit <= 0 {
		limit = 5
	}
	if window <= 0 {
		window = 10 * time.Minute
	}
	return &RegisterThrottle{
		limit:       limit,
		window:      window,
		now:         func() time.Time { return time.Now().UTC() },
		clientsByIP: make(map[string]throttleWindow),
	}
}

func (t *RegisterThrottle) SetNowFunc(now func() time.Time) {
	if t == nil || now == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.now = now
}

func (t *RegisterThrottle) Allow(ip string) (bool, time.Duration) {
	if t == nil || ip == "" {
		return true, 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	window := t.currentWindowLocked(ip, now)
	if window.failures < t.limit {
		return true, 0
	}
	retryAfter := window.start.Add(t.window).Sub(now)
	if retryAfter < 0 {
		retryAfter = 0
	}
	return false, retryAfter
}

func (t *RegisterThrottle) RecordFailure(ip string) {
	if t == nil || ip == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	window := t.currentWindowLocked(ip, now)
	window.failures++
	t.clientsByIP[ip] = window
}

func (t *RegisterThrottle) Reset(ip string) {
	if t == nil || ip == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.clientsByIP, ip)
}

func (t *RegisterThrottle) currentWindowLocked(ip string, now time.Time) throttleWindow {
	window, ok := t.clientsByIP[ip]
	if !ok || now.Sub(window.start) >= t.window {
		window = throttleWindow{start: now}
		t.clientsByIP[ip] = window
	}
	return window
}
