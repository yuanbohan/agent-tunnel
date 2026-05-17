package direct

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunDirectFirstReturnsDirectOnSuccess(t *testing.T) {
	result, err := RunDirectFirst(context.Background(), func(context.Context) error {
		return nil
	}, nil, time.Second)
	if err != nil {
		t.Fatalf("RunDirectFirst returned error: %v", err)
	}
	if result.PathKind != "direct" || result.FallbackReason != "" {
		t.Fatalf("result = %#v, want direct without fallback reason", result)
	}
}

func TestRunDirectFirstFallsBackAfterTimeout(t *testing.T) {
	fallbackReason := ""
	result, err := RunDirectFirst(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}, func(_ context.Context, reason string) error {
		fallbackReason = reason
		return nil
	}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("RunDirectFirst returned error: %v", err)
	}
	if result.PathKind != "relay" || result.FallbackReason != FallbackReasonDirectTimeout || fallbackReason != FallbackReasonDirectTimeout {
		t.Fatalf("result = %#v fallbackReason=%q, want relay direct_timeout", result, fallbackReason)
	}
}

func TestRunDirectFirstFallsBackAfterDirectError(t *testing.T) {
	directErr := errors.New("udp blocked")
	result, err := RunDirectFirst(context.Background(), func(context.Context) error {
		return directErr
	}, func(context.Context, string) error {
		return nil
	}, time.Second)
	if err != nil {
		t.Fatalf("RunDirectFirst returned error: %v", err)
	}
	if result.PathKind != "relay" || result.FallbackReason != FallbackReasonDirectUnreachable {
		t.Fatalf("result = %#v, want relay direct_unreachable", result)
	}
}

func TestRunDirectFirstPropagatesFallbackError(t *testing.T) {
	fallbackErr := errors.New("relay failed")
	result, err := RunDirectFirst(context.Background(), nil, func(context.Context, string) error {
		return fallbackErr
	}, time.Second)
	if !errors.Is(err, fallbackErr) {
		t.Fatalf("err = %v, want fallbackErr", err)
	}
	if result.PathKind != "relay" || result.FallbackReason != FallbackReasonDirectSkipped {
		t.Fatalf("result = %#v, want skipped relay result", result)
	}
}
