package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/hanan/avatar-catalog-backend/internal/apierr"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

// RequestIDFrom returns the request ID attached by the requestID middleware.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// requestID assigns every request an ID, reusing an inbound X-Request-ID when
// the caller supplies one, and echoes it back on the response.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			var b [8]byte
			if _, err := rand.Read(b[:]); err == nil {
				id = hex.EncodeToString(b[:])
			}
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// statusRecorder captures the status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// accessLog emits one structured line per request.
func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFrom(r.Context()),
			)
		})
	}
}

// recoverPanic turns a handler panic into a 500 instead of killing the server.
func recoverPanic(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"err", rec,
						"path", r.URL.Path,
						"request_id", RequestIDFrom(r.Context()),
					)
					writeError(w, apierr.New(http.StatusInternalServerError, "internal_error", "Terjadi kesalahan di sisi server"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// chain applies middlewares so the first listed runs outermost.
func chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
