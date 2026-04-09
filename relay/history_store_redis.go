package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisHistoryStoreConfig struct {
	TTL      time.Duration
	MaxBytes int
}

type RedisHistoryStore struct {
	client   *redis.Client
	ttl      time.Duration
	maxBytes int
}

type redisHistoryState struct {
	firstSeq  uint64
	latestSeq uint64
}

func NewRedisHistoryStore(client *redis.Client, cfg RedisHistoryStoreConfig) *RedisHistoryStore {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultHistoryTTL
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = maxSessionHistoryBytes
	}
	return &RedisHistoryStore{
		client:   client,
		ttl:      ttl,
		maxBytes: maxBytes,
	}
}

func (s *RedisHistoryStore) LatestSeq(sessionID string) (uint64, bool, error) {
	state, ok, err := s.loadState(context.Background(), sessionID)
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, nil
	}
	return state.latestSeq, true, nil
}

func (s *RedisHistoryStore) AppendFrame(sessionID string, chunk []byte, cols, rows int, ts time.Time) (uint64, error) {
	payload, err := json.Marshal(newHistoryFramePayload(chunk, cols, rows, ts))
	if err != nil {
		return 0, err
	}

	keys := []string{
		historyFramesKey(sessionID),
		historySizesKey(sessionID),
		historyMetaKey(sessionID),
	}
	ttlSeconds := int64(s.ttl / time.Second)
	if ttlSeconds <= 0 {
		ttlSeconds = 1
	}

	result, err := redisAppendFrameScript.Run(context.Background(), s.client, keys, string(payload), len(chunk), ttlSeconds, s.maxBytes).Result()
	if err != nil {
		return 0, err
	}

	seq, err := parseUint64Value(result)
	if err != nil {
		return 0, fmt.Errorf("parse append seq: %w", err)
	}
	return seq, nil
}

func (s *RedisHistoryStore) Frames(sessionID string, from uint64, hasFrom bool, to uint64, hasTo bool) ([]outputFrameMessage, bool, error) {
	ctx := context.Background()
	state, ok, err := s.loadState(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	startSeq := state.firstSeq
	if hasFrom && from > startSeq {
		startSeq = from
	}
	endSeq := state.latestSeq
	if hasTo && to < endSeq {
		endSeq = to
	}
	if startSeq > endSeq {
		return []outputFrameMessage{}, true, nil
	}

	startIdx := int64(startSeq - state.firstSeq)
	endIdx := int64(endSeq - state.firstSeq)
	payloads, err := s.client.LRange(ctx, historyFramesKey(sessionID), startIdx, endIdx).Result()
	if err != nil {
		return nil, false, err
	}
	if len(payloads) == 0 {
		if err := s.clearHistory(ctx, sessionID); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	decoded := make([]historyFramePayload, 0, len(payloads))
	for _, raw := range payloads {
		var payload historyFramePayload
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, false, err
		}
		decoded = append(decoded, payload)
	}
	return historyFrameMessages(startSeq, decoded), true, nil
}

func (s *RedisHistoryStore) loadState(ctx context.Context, sessionID string) (redisHistoryState, bool, error) {
	pipe := s.client.Pipeline()
	metaCmd := pipe.HMGet(ctx, historyMetaKey(sessionID), "first_seq", "latest_seq", "frame_bytes")
	framesLenCmd := pipe.LLen(ctx, historyFramesKey(sessionID))
	sizesLenCmd := pipe.LLen(ctx, historySizesKey(sessionID))
	if _, err := pipe.Exec(ctx); err != nil {
		return redisHistoryState{}, false, err
	}

	values := metaCmd.Val()
	framesLen := framesLenCmd.Val()
	sizesLen := sizesLenCmd.Val()
	metaPresent := false
	for _, value := range values {
		if value != nil {
			metaPresent = true
			break
		}
	}

	if !metaPresent && framesLen == 0 && sizesLen == 0 {
		return redisHistoryState{}, false, nil
	}

	inconsistent := len(values) < 3 ||
		!metaPresent ||
		values[0] == nil ||
		values[1] == nil ||
		values[2] == nil ||
		framesLen == 0 ||
		sizesLen == 0 ||
		framesLen != sizesLen
	if inconsistent {
		if err := s.clearHistory(ctx, sessionID); err != nil {
			return redisHistoryState{}, false, err
		}
		return redisHistoryState{}, false, nil
	}

	firstSeq, err := parseUint64Value(values[0])
	if err != nil {
		return redisHistoryState{}, false, fmt.Errorf("parse first_seq: %w", err)
	}
	latestSeq, err := parseUint64Value(values[1])
	if err != nil {
		return redisHistoryState{}, false, fmt.Errorf("parse latest_seq: %w", err)
	}
	if _, err := parseUint64Value(values[2]); err != nil {
		return redisHistoryState{}, false, fmt.Errorf("parse frame_bytes: %w", err)
	}

	if latestSeq < firstSeq || int64(latestSeq-firstSeq+1) != framesLen {
		if err := s.clearHistory(ctx, sessionID); err != nil {
			return redisHistoryState{}, false, err
		}
		return redisHistoryState{}, false, nil
	}

	return redisHistoryState{
		firstSeq:  firstSeq,
		latestSeq: latestSeq,
	}, true, nil
}

func (s *RedisHistoryStore) clearHistory(ctx context.Context, sessionID string) error {
	return s.client.Del(ctx, historyFramesKey(sessionID), historySizesKey(sessionID), historyMetaKey(sessionID)).Err()
}

func historyFramesKey(sessionID string) string {
	return fmt.Sprintf("agentunnel:frames:{%s}", sessionID)
}

func historySizesKey(sessionID string) string {
	return fmt.Sprintf("agentunnel:frames:{%s}:sizes", sessionID)
}

func historyMetaKey(sessionID string) string {
	return fmt.Sprintf("agentunnel:frames:{%s}:meta", sessionID)
}

func parseUint64Value(value any) (uint64, error) {
	switch typed := value.(type) {
	case int64:
		if typed < 0 {
			return 0, fmt.Errorf("negative int64 %d", typed)
		}
		return uint64(typed), nil
	case string:
		var parsed uint64
		_, err := fmt.Sscan(typed, &parsed)
		return parsed, err
	case []byte:
		var parsed uint64
		_, err := fmt.Sscan(string(typed), &parsed)
		return parsed, err
	default:
		return 0, fmt.Errorf("unsupported value type %T", value)
	}
}

var redisAppendFrameScript = redis.NewScript(`
local frames_key = KEYS[1]
local sizes_key = KEYS[2]
local meta_key = KEYS[3]

local payload = ARGV[1]
local size = tonumber(ARGV[2])
local ttl_seconds = tonumber(ARGV[3])
local max_bytes = tonumber(ARGV[4])

local first_raw = redis.call('HGET', meta_key, 'first_seq')
local latest_raw = redis.call('HGET', meta_key, 'latest_seq')
local frame_bytes_raw = redis.call('HGET', meta_key, 'frame_bytes')
local frames_len = redis.call('LLEN', frames_key)
local sizes_len = redis.call('LLEN', sizes_key)

local function reset_state()
	redis.call('DEL', frames_key, sizes_key, meta_key)
	first_raw = false
	latest_raw = false
	frame_bytes_raw = false
	frames_len = 0
	sizes_len = 0
end

if frames_len ~= sizes_len then
	reset_state()
elseif first_raw or latest_raw or frame_bytes_raw or frames_len > 0 or sizes_len > 0 then
	if (not first_raw) or (not latest_raw) or (not frame_bytes_raw) or frames_len == 0 then
		reset_state()
	else
		local first_num = tonumber(first_raw)
		local latest_num = tonumber(latest_raw)
		local frame_bytes_num = tonumber(frame_bytes_raw)
		if (not first_num) or (not latest_num) or (not frame_bytes_num) or latest_num < first_num or (latest_num - first_num + 1) ~= frames_len then
			reset_state()
		end
	end
end

local latest = tonumber(latest_raw or '0')
local first = tonumber(first_raw or '0')
local frame_bytes = tonumber(frame_bytes_raw or '0')

local seq = latest + 1
if first == 0 then
	first = seq
end

redis.call('RPUSH', frames_key, payload)
redis.call('RPUSH', sizes_key, size)
frame_bytes = frame_bytes + size

while frame_bytes > max_bytes and redis.call('LLEN', frames_key) > 1 do
	local oldest_size = tonumber(redis.call('LPOP', sizes_key) or '0')
	redis.call('LPOP', frames_key)
	frame_bytes = frame_bytes - oldest_size
	first = first + 1
end

redis.call('HSET', meta_key,
	'first_seq', first,
	'latest_seq', seq,
	'frame_bytes', frame_bytes)

redis.call('EXPIRE', frames_key, ttl_seconds)
redis.call('EXPIRE', sizes_key, ttl_seconds)
redis.call('EXPIRE', meta_key, ttl_seconds)

return seq
`)
