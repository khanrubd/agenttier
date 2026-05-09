/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package router

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	// ClaimsContextKey is the context key for authenticated user claims.
	ClaimsContextKey contextKey = "claims"
	// RequestIDContextKey is the context key for the request ID.
	RequestIDContextKey contextKey = "requestID"
)

// Claims represents the authenticated user's identity extracted from JWT or API key.
type Claims struct {
	Sub        string   `json:"sub"`
	Email      string   `json:"email"`
	Name       string   `json:"name"`
	Groups     []string `json:"groups"`
	IsAdmin    bool     `json:"isAdmin"`
	Namespaces []string `json:"namespaces,omitempty"` // For API key scoping
}

// GetClaims extracts the authenticated claims from the request context.
func GetClaims(ctx context.Context) *Claims {
	claims, _ := ctx.Value(ClaimsContextKey).(*Claims)
	return claims
}

// requestIDMiddleware adds a unique request ID to each request.
func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), RequestIDContextKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// corsMiddleware handles Cross-Origin Resource Sharing headers.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*") // TODO: Configure allowed origins
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs each request with duration and status.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
	})
}

// authMiddleware validates JWT tokens or API keys and injects claims into context.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var claims *Claims
		var err error

		// Dev mode: if no OIDC issuer configured, allow unauthenticated access with a default identity
		if s.config.OIDCIssuerURL == "" {
			claims = &Claims{
				Sub:     "dev-user",
				Email:   "dev@agenttier.local",
				Name:    "Dev User",
				Groups:  []string{"agenttier-admins"},
				IsAdmin: true,
			}
			ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Try API key first (X-API-Key header)
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "" {
			claims, err = s.validateAPIKey(r.Context(), apiKey)
			if err != nil {
				http.Error(w, `{"error":"invalid_api_key"}`, http.StatusUnauthorized)
				return
			}
		} else {
			// Try Bearer token (OIDC JWT)
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error":"missing_authorization"}`, http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == authHeader {
				http.Error(w, `{"error":"invalid_authorization_format"}`, http.StatusUnauthorized)
				return
			}

			claims, err = s.validateJWT(r.Context(), token)
			if err != nil {
				http.Error(w, `{"error":"invalid_token","message":"`+err.Error()+`"}`, http.StatusUnauthorized)
				return
			}
		}

		// Inject claims into context
		ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// validateJWT validates an OIDC JWT token and returns claims.
func (s *Server) validateJWT(ctx context.Context, token string) (*Claims, error) {
	// TODO: Implement full OIDC JWT validation with JWKS caching
	// For now, return a placeholder implementation
	// In production: parse JWT, validate signature against JWKS, check exp/iss/aud
	_ = ctx
	_ = token
	return nil, fmt.Errorf("OIDC validation not yet implemented")
}

// validateAPIKey validates an API key against MongoDB.
func (s *Server) validateAPIKey(ctx context.Context, key string) (*Claims, error) {
	// TODO: Implement API key validation with MongoDB lookup + LRU cache
	_ = ctx
	_ = key
	return nil, fmt.Errorf("API key validation not yet implemented")
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker for WebSocket support.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("upstream ResponseWriter does not implement http.Hijacker")
}

// Flush implements http.Flusher.
func (rw *responseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// generateRequestID creates a random hex request ID.
func generateRequestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
