package gateway

import (
	"net/http"
	"strings"
)

// authMiddleware rejects requests without a valid Bearer token.
// If the server's APIKey is empty (dev mode), all requests pass through.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		if token == auth || !s.validateAPIKey(token) {
			http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// validateAPIKey checks whether key matches the configured API key.
func (s *Server) validateAPIKey(key string) bool {
	return s.cfg.APIKey != "" && key == s.cfg.APIKey
}
