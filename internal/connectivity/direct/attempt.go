package direct

import (
	"context"
	"errors"
	"time"
)

const DefaultAttemptDeadline = 3 * time.Second

const (
	FallbackReasonDirectSkipped     = "direct_skipped"
	FallbackReasonDirectTimeout     = "direct_timeout"
	FallbackReasonDirectUnreachable = "direct_unreachable"
)

type AttemptResult struct {
	PathKind        string
	FallbackReason  string
	DirectLatency   time.Duration
	FallbackLatency time.Duration
}

type AttemptFunc func(context.Context) error
type FallbackFunc func(context.Context, string) error

func RunDirectFirst(ctx context.Context, direct AttemptFunc, fallback FallbackFunc, deadline time.Duration) (AttemptResult, error) {
	if deadline <= 0 {
		deadline = DefaultAttemptDeadline
	}
	if direct == nil {
		return runFallback(ctx, fallback, FallbackReasonDirectSkipped, 0)
	}

	start := time.Now()
	directCtx, cancel := context.WithTimeout(ctx, deadline)
	err := direct(directCtx)
	cancel()
	directLatency := time.Since(start)
	if err == nil {
		return AttemptResult{PathKind: "direct", DirectLatency: directLatency}, nil
	}
	reason := FallbackReasonDirectUnreachable
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(directCtx.Err(), context.DeadlineExceeded) {
		reason = FallbackReasonDirectTimeout
	}
	return runFallback(ctx, fallback, reason, directLatency)
}

func runFallback(ctx context.Context, fallback FallbackFunc, reason string, directLatency time.Duration) (AttemptResult, error) {
	if fallback == nil {
		return AttemptResult{PathKind: "relay", FallbackReason: reason, DirectLatency: directLatency}, nil
	}
	start := time.Now()
	if err := fallback(ctx, reason); err != nil {
		return AttemptResult{PathKind: "relay", FallbackReason: reason, DirectLatency: directLatency, FallbackLatency: time.Since(start)}, err
	}
	return AttemptResult{PathKind: "relay", FallbackReason: reason, DirectLatency: directLatency, FallbackLatency: time.Since(start)}, nil
}
