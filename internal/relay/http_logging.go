package relay

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"
)

func logRequests(logger *Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		startedAt := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w}
		logged := false
		logRequest := func() {
			if logged {
				return
			}
			logged = true

			fields := []Field{
				String("method", r.Method),
				String("path", r.URL.Path),
				String("target", requestTarget(r)),
				Int("status", lw.Status()),
				Int64("duration_ms", time.Since(startedAt).Milliseconds()),
			}
			fields = append(fields, requestLogFields(r)...)
			if r.ContentLength >= 0 {
				fields = append(fields, Int64("request_bytes", r.ContentLength))
			}
			if !lw.hijacked || lw.bytesWritten > 0 {
				fields = append(fields, Int64("response_bytes", lw.bytesWritten))
			}

			logger.Info("http_request_completed", fields...)
		}

		lw.onSwitchingProtocols = logRequest
		next.ServeHTTP(lw, r)
		logRequest()
	})
}

func requestTarget(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.URL.RequestURI()
}

func requestLogFields(r *http.Request) []Field {
	fields := []Field{
		String("remote_addr", r.RemoteAddr),
		String("user_agent", r.UserAgent()),
	}
	if requestID := requestIDFromRequest(r); requestID != "" {
		fields = append(fields, String("request_id", requestID))
	}
	return fields
}

func requestIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Header.Get("X-Request-Id")
}

type loggingResponseWriter struct {
	http.ResponseWriter

	status               int
	wroteHeader          bool
	hijacked             bool
	bytesWritten         int64
	onSwitchingProtocols func()
}

func (w *loggingResponseWriter) Status() int {
	if w.wroteHeader {
		return w.status
	}
	return http.StatusOK
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
	if status == http.StatusSwitchingProtocols && w.onSwitchingProtocols != nil {
		w.onSwitchingProtocols()
	}
}

func (w *loggingResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytesWritten += int64(n)
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}

	if !w.wroteHeader {
		w.status = http.StatusSwitchingProtocols
		w.wroteHeader = true
	}
	w.hijacked = true
	if w.onSwitchingProtocols != nil {
		w.onSwitchingProtocols()
	}

	return conn, rw, nil
}

func (w *loggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
