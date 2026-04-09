package relay

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisHistoryStoreAppendAndLatestSeq(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisHistoryStore(client, RedisHistoryStoreConfig{
		TTL:      24 * time.Hour,
		MaxBytes: maxSessionHistoryBytes,
	})

	ts := time.Unix(100, 0).UTC()
	seq, err := store.AppendFrame("sess-1", []byte("hello\n"), 120, 40, ts)
	if err != nil {
		t.Fatalf("AppendFrame returned error: %v", err)
	}
	if seq != 1 {
		t.Fatalf("seq = %d, want 1", seq)
	}

	latestSeq, ok, err := store.LatestSeq("sess-1")
	if err != nil {
		t.Fatalf("LatestSeq returned error: %v", err)
	}
	if !ok {
		t.Fatal("LatestSeq ok = false, want true")
	}
	if latestSeq != 1 {
		t.Fatalf("LatestSeq = %d, want 1", latestSeq)
	}
}

func TestRedisHistoryStoreFramesInclusiveRange(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisHistoryStore(client, RedisHistoryStoreConfig{
		TTL:      24 * time.Hour,
		MaxBytes: maxSessionHistoryBytes,
	})

	for i, payload := range []string{"one", "two", "three"} {
		if _, err := store.AppendFrame("sess-1", []byte(payload), 120+i, 40+i, time.Unix(int64(100+i), 0).UTC()); err != nil {
			t.Fatalf("AppendFrame #%d returned error: %v", i+1, err)
		}
	}

	frames, ok, err := store.Frames("sess-1", 2, true, 3, true)
	if err != nil {
		t.Fatalf("Frames returned error: %v", err)
	}
	if !ok {
		t.Fatal("Frames ok = false, want true")
	}
	if len(frames) != 2 {
		t.Fatalf("len(Frames) = %d, want 2", len(frames))
	}
	if frames[0].Seq != 2 || frames[1].Seq != 3 {
		t.Fatalf("seqs = %#v, want 2 then 3", frames)
	}
}

func TestRedisHistoryStoreTrimAdvancesFirstSeq(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisHistoryStore(client, RedisHistoryStoreConfig{
		TTL:      24 * time.Hour,
		MaxBytes: 5,
	})

	for i, payload := range []string{"abc", "def", "ghi"} {
		if _, err := store.AppendFrame("sess-1", []byte(payload), 120, 40, time.Unix(int64(100+i), 0).UTC()); err != nil {
			t.Fatalf("AppendFrame #%d returned error: %v", i+1, err)
		}
	}

	frames, ok, err := store.Frames("sess-1", 0, false, 0, false)
	if err != nil {
		t.Fatalf("Frames returned error: %v", err)
	}
	if !ok {
		t.Fatal("Frames ok = false, want true")
	}
	if len(frames) != 1 {
		t.Fatalf("len(Frames) = %d, want 1", len(frames))
	}
	if frames[0].Seq != 3 {
		t.Fatalf("Seq = %d, want 3", frames[0].Seq)
	}
}

func TestRedisHistoryStoreTTLExpiryRemovesHistory(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisHistoryStore(client, RedisHistoryStoreConfig{
		TTL:      time.Hour,
		MaxBytes: maxSessionHistoryBytes,
	})

	if _, err := store.AppendFrame("sess-1", []byte("hello"), 120, 40, time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("AppendFrame returned error: %v", err)
	}

	srv.FastForward(2 * time.Hour)

	_, ok, err := store.LatestSeq("sess-1")
	if err != nil {
		t.Fatalf("LatestSeq returned error: %v", err)
	}
	if ok {
		t.Fatal("LatestSeq ok = true after expiry, want false")
	}
}

func TestRedisHistoryStoreRehydratesAfterNewInstance(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisHistoryStore(client, RedisHistoryStoreConfig{
		TTL:      24 * time.Hour,
		MaxBytes: maxSessionHistoryBytes,
	})

	if _, err := store.AppendFrame("sess-1", []byte("one"), 120, 40, time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("AppendFrame returned error: %v", err)
	}
	if _, err := store.AppendFrame("sess-1", []byte("two"), 121, 41, time.Unix(101, 0).UTC()); err != nil {
		t.Fatalf("AppendFrame returned error: %v", err)
	}

	reloaded := NewRedisHistoryStore(redis.NewClient(&redis.Options{Addr: srv.Addr()}), RedisHistoryStoreConfig{
		TTL:      24 * time.Hour,
		MaxBytes: maxSessionHistoryBytes,
	})

	latestSeq, ok, err := reloaded.LatestSeq("sess-1")
	if err != nil {
		t.Fatalf("LatestSeq returned error: %v", err)
	}
	if !ok {
		t.Fatal("LatestSeq ok = false, want true")
	}
	if latestSeq != 2 {
		t.Fatalf("LatestSeq = %d, want 2", latestSeq)
	}
}

func TestRedisHistoryStoreTreatsOrphanedMetaAsMissing(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisHistoryStore(client, RedisHistoryStoreConfig{
		TTL:      24 * time.Hour,
		MaxBytes: maxSessionHistoryBytes,
	})

	if _, err := store.AppendFrame("sess-1", []byte("one"), 120, 40, time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("AppendFrame returned error: %v", err)
	}
	if err := client.Del(context.Background(), historyFramesKey("sess-1"), historySizesKey("sess-1")).Err(); err != nil {
		t.Fatalf("Del returned error: %v", err)
	}

	_, ok, err := store.LatestSeq("sess-1")
	if err != nil {
		t.Fatalf("LatestSeq returned error: %v", err)
	}
	if ok {
		t.Fatal("LatestSeq ok = true, want false after orphaned meta cleanup")
	}

	frames, ok, err := store.Frames("sess-1", 0, false, 0, false)
	if err != nil {
		t.Fatalf("Frames returned error: %v", err)
	}
	if ok {
		t.Fatalf("Frames ok = true with frames %#v, want false after orphaned meta cleanup", frames)
	}
	if srv.Exists(historyMetaKey("sess-1")) {
		t.Fatal("meta key still exists after orphaned meta cleanup")
	}
}

func TestRedisHistoryStoreAppendResetsOrphanedMetaState(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	store := NewRedisHistoryStore(client, RedisHistoryStoreConfig{
		TTL:      24 * time.Hour,
		MaxBytes: maxSessionHistoryBytes,
	})

	if _, err := store.AppendFrame("sess-1", []byte("one"), 120, 40, time.Unix(100, 0).UTC()); err != nil {
		t.Fatalf("AppendFrame returned error: %v", err)
	}
	if err := client.Del(context.Background(), historyFramesKey("sess-1"), historySizesKey("sess-1")).Err(); err != nil {
		t.Fatalf("Del returned error: %v", err)
	}

	seq, err := store.AppendFrame("sess-1", []byte("two"), 121, 41, time.Unix(101, 0).UTC())
	if err != nil {
		t.Fatalf("AppendFrame returned error: %v", err)
	}
	if seq != 1 {
		t.Fatalf("seq = %d, want 1 after resetting orphaned state", seq)
	}

	frames, ok, err := store.Frames("sess-1", 0, false, 0, false)
	if err != nil {
		t.Fatalf("Frames returned error: %v", err)
	}
	if !ok {
		t.Fatal("Frames ok = false, want true")
	}
	if len(frames) != 1 || frames[0].Seq != 1 {
		t.Fatalf("frames = %#v, want exactly one frame at seq 1", frames)
	}
}
