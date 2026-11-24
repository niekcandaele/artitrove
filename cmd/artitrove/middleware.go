package main

import "net/http"

// securityHeaders sets the response headers every response carries. They are set before
// next runs, so they are also present on mux 404 and 405 responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Robots-Tag", "noindex")
		next.ServeHTTP(w, r)
	})
}
