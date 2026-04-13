package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type authMiddlewareDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) interface{ Scan(dest ...any) error }
}

func JWTAuthMiddleware(jwtSvc *JwtService, pool *pgxpool.Pool) func(http.Handler) http.Handler {
	publicPrefixes := []string{
		"/api/auth/",
		"/api/debug/",
		"/api/health",
		"/api/test-tool/",
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// OPTIONS preflight
			if r.Method == "OPTIONS" {
				next.ServeHTTP(w, r)
				return
			}

			// Non-API paths pass through
			if !strings.HasPrefix(path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			// Public paths pass through
			for _, prefix := range publicPrefixes {
				if strings.HasPrefix(path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Extract Bearer token
			token := extractBearerToken(r)
			if token == "" {
				writeAuthError(w, http.StatusUnauthorized, 40102, "missing authorization token")
				return
			}

			userID, err := jwtSvc.ParseUserID(token)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, 40102, "invalid or expired token")
				return
			}

			ctx := WithUserID(r.Context(), userID)

			// Admin route protection
			if strings.HasPrefix(path, "/api/admin/") {
				var isAdmin bool
				err := pool.QueryRow(ctx, "SELECT COALESCE(is_admin, false) FROM users WHERE id = $1", userID).Scan(&isAdmin)
				if err != nil || !isAdmin {
					writeAuthError(w, http.StatusForbidden, 40302, "admin access required")
					return
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func writeAuthError(w http.ResponseWriter, status int, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"code":    code,
		"message": message,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to write auth error response", "err", err)
	}
}
