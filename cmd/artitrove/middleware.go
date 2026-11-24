package main

import (
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// defaultReadTimeout is a variable so tests can shorten it.
var defaultReadTimeout = 30 * time.Second

// securityHeaders sets the response headers every response carries. They are set before
// next runs, so they are also present on mux 404 and 405 responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unauthenticated clients must not hold a connection by never sending the declared body.
		// An authenticated upload extends this deadline.
		if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(defaultReadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			slog.Error("set read deadline", "error", err)
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex")
		next.ServeHTTP(w, r)
	})
}
