package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthenticateWithSoccer(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		rejected   bool
		wantErr    bool
		errorIs    error
	}{
		{
			name:       "authenticated user",
			statusCode: http.StatusOK,
			body:       `{"code":0,"data":{"user":{"id":42,"username":"player","nickname":"Player One","status":1}}}`,
		},
		{
			name:       "invalid credentials",
			statusCode: http.StatusUnauthorized,
			body:       `{"code":1002,"message":"账号或密码错误","data":null}`,
			rejected:   true,
		},
		{
			name:       "invalid response",
			statusCode: http.StatusBadGateway,
			body:       `not-json`,
			wantErr:    true,
		},
		{
			name:       "disabled account",
			statusCode: http.StatusForbidden,
			body:       `{"code":1002,"message":"账号已被禁用","data":null}`,
			wantErr:    true,
			errorIs:    errSoccerAccountDisabled,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{"code":429,"message":"请求过于频繁，请稍后再试","data":null}`,
			wantErr:    true,
			errorIs:    errSoccerRateLimited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			a := &app{
				config: config{soccerAuthURL: server.URL},
				http:   &http.Client{Timeout: time.Second},
			}
			got, rejected, err := a.authenticateWithSoccer(context.Background(), "player", "password")
			if (err != nil) != tt.wantErr {
				t.Fatalf("authenticateWithSoccer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errorIs != nil && !errors.Is(err, tt.errorIs) {
				t.Fatalf("authenticateWithSoccer() error = %v, want %v", err, tt.errorIs)
			}
			if rejected != tt.rejected {
				t.Fatalf("authenticateWithSoccer() rejected = %v, want %v", rejected, tt.rejected)
			}
			if !tt.wantErr && !tt.rejected && (got.SoccerUserID != 42 || got.Username != "player" || got.Nickname != "Player One") {
				t.Fatalf("authenticateWithSoccer() user = %#v", got)
			}
		})
	}
}

func TestAuthRequiresPlatformIssuer(t *testing.T) {
	a := &app{config: config{jwtSecret: "test-secret"}}
	protected := a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentUserID(r) != 42 {
			t.Fatalf("currentUserID() = %d", currentUserID(r))
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	validToken, err := a.issueToken(user{ID: 42})
	if err != nil {
		t.Fatalf("issueToken() error = %v", err)
	}
	validRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	validRequest.Header.Set("Authorization", "Bearer "+validToken)
	validResponse := httptest.NewRecorder()
	protected.ServeHTTP(validResponse, validRequest)
	if validResponse.Code != http.StatusNoContent {
		t.Fatalf("valid token status = %d", validResponse.Code)
	}

	legacyToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		UserID: 42,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(42, 10),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString([]byte(a.config.jwtSecret))
	if err != nil {
		t.Fatalf("sign legacy token error = %v", err)
	}
	legacyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	legacyRequest.Header.Set("Authorization", "Bearer "+legacyToken)
	legacyResponse := httptest.NewRecorder()
	protected.ServeHTTP(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusUnauthorized {
		t.Fatalf("legacy token status = %d, want %d", legacyResponse.Code, http.StatusUnauthorized)
	}
}

func TestDefaultCORSOriginsIncludeTauriWindows(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "")

	cfg := loadConfig()
	if !aOriginAllowed(cfg, "http://tauri.localhost") {
		t.Fatal("default CORS origins do not allow the Windows Tauri webview")
	}
}
