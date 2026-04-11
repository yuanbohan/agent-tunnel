package logx

import (
	"context"
	"io"
	"log/slog"
	"sync"
)

type Field = slog.Attr

var setupMu sync.Mutex

func String(key, value string) Field {
	return slog.String(key, value)
}

func Int64(key string, value int64) Field {
	return slog.Int64(key, value)
}

func Int(key string, value int) Field {
	return slog.Int(key, value)
}

func Setup(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	slog.SetDefault(slog.New(newJSONHandler(w)))
}

func UseWriterForTest(w io.Writer) func() {
	setupMu.Lock()
	prev := slog.Default()
	Setup(w)
	return func() {
		slog.SetDefault(prev)
		setupMu.Unlock()
	}
}

func Info(event string, fields ...Field) {
	log(slog.LevelInfo, event, fields...)
}

func Warn(event string, fields ...Field) {
	log(slog.LevelWarn, event, fields...)
}

func Error(event string, fields ...Field) {
	log(slog.LevelError, event, fields...)
}

func log(level slog.Level, event string, fields ...Field) {
	attrs := make([]slog.Attr, 0, len(fields)+1)
	attrs = append(attrs, slog.String("event", event))
	for _, field := range fields {
		if field.Key == "event" {
			continue
		}
		attrs = append(attrs, field)
	}

	slog.LogAttrs(context.Background(), level, "", attrs...)
}

func newJSONHandler(w io.Writer) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Key = "ts"
				return a
			}
			if a.Key == slog.MessageKey && a.Value.Kind() == slog.KindString && a.Value.String() == "" {
				return slog.Attr{}
			}
			return a
		},
	})
}
