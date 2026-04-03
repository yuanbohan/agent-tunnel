package relay

import (
	"context"
	"io"
	"log/slog"
)

type Field = slog.Attr

func String(key, value string) Field {
	return slog.String(key, value)
}

func Int64(key string, value int64) Field {
	return slog.Int64(key, value)
}

type Logger struct {
	logger *slog.Logger
}

func NewLogger(w io.Writer) *Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.MessageKey && a.Value.Kind() == slog.KindString && a.Value.String() == "" {
				return slog.Attr{}
			}
			return a
		},
	})
	return &Logger{logger: slog.New(handler)}
}

func NewDiscardLogger() *Logger {
	return NewLogger(io.Discard)
}

func (l *Logger) Info(msg string, fields ...Field) {
	l.log(slog.LevelInfo, msg, fields...)
}

func (l *Logger) Warn(msg string, fields ...Field) {
	l.log(slog.LevelWarn, msg, fields...)
}

func (l *Logger) Error(msg string, fields ...Field) {
	l.log(slog.LevelError, msg, fields...)
}

func (l *Logger) log(level slog.Level, msg string, fields ...Field) {
	if l == nil || l.logger == nil {
		return
	}

	attrs := make([]slog.Attr, 0, len(fields)+1)
	if msg != "" {
		hasEvent := false
		for _, field := range fields {
			if field.Key == "event" {
				hasEvent = true
				break
			}
		}
		if !hasEvent {
			attrs = append(attrs, slog.String("event", msg))
		}
	}
	attrs = append(attrs, fields...)

	l.logger.LogAttrs(context.Background(), level, "", attrs...)
}
