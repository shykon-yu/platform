package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
			body:       `{"code":0,"data":{"user":{"id":42,"username":"player","nickname":"Player One","status":1,"platform_access_expires_at":"2099-12-31T23:59:59+08:00"}}}`,
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
			name:       "platform access expired",
			statusCode: http.StatusForbidden,
			body:       `{"code":1008,"message":"平台使用权限已到期，请联系管理员","data":null}`,
			wantErr:    true,
			errorIs:    errSoccerPlatformExpired,
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

func TestIssueTokenDoesNotOutlivePlatformAccess(t *testing.T) {
	accessExpiresAt := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	a := &app{config: config{jwtSecret: "test-secret", jwtAudience: "test-server"}}

	tokenString, err := a.issueToken(user{ID: 42, SessionID: "current-session", PlatformAccessExpiresAt: accessExpiresAt})
	if err != nil {
		t.Fatalf("issueToken() error = %v", err)
	}
	parsed, err := jwt.ParseWithClaims(tokenString, &claims{}, func(_ *jwt.Token) (any, error) {
		return []byte("test-secret"), nil
	})
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	got := parsed.Claims.(*claims).ExpiresAt.Time
	if !got.Equal(accessExpiresAt) {
		t.Fatalf("token expiry = %v, want %v", got, accessExpiresAt)
	}
}

func TestAuthRequiresPlatformIssuer(t *testing.T) {
	a := &app{
		config: config{jwtSecret: "test-secret", jwtAudience: "test-server"},
		validateSession: func(_ context.Context, userID int64, sessionID string) (bool, error) {
			return userID == 42 && sessionID == "current-session", nil
		},
	}
	protected := a.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentUserID(r) != 42 {
			t.Fatalf("currentUserID() = %d", currentUserID(r))
		}
		if currentSessionID(r) != "current-session" {
			t.Fatalf("currentSessionID() = %q", currentSessionID(r))
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	validToken, err := a.issueToken(user{ID: 42, SessionID: "current-session"})
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

	otherServer := &app{config: config{jwtSecret: "test-secret", jwtAudience: "other-server"}}
	otherServerToken, err := otherServer.issueToken(user{ID: 42, SessionID: "current-session"})
	if err != nil {
		t.Fatalf("issue other server token: %v", err)
	}
	otherServerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	otherServerRequest.Header.Set("Authorization", "Bearer "+otherServerToken)
	otherServerResponse := httptest.NewRecorder()
	protected.ServeHTTP(otherServerResponse, otherServerRequest)
	if otherServerResponse.Code != http.StatusUnauthorized {
		t.Fatalf("other server token status = %d, want %d", otherServerResponse.Code, http.StatusUnauthorized)
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

func TestAuthRejectsReplacedSession(t *testing.T) {
	a := &app{
		config: config{jwtSecret: "test-secret", jwtAudience: "test-server"},
		validateSession: func(_ context.Context, _ int64, _ string) (bool, error) {
			return false, nil
		},
	}
	protected := a.auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("replaced session reached protected handler")
	}))
	token, err := a.issueToken(user{ID: 42, SessionID: "old-session"})
	if err != nil {
		t.Fatalf("issueToken() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("replaced session status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if body := response.Body.String(); !strings.Contains(body, `"code":"SESSION_REPLACED"`) {
		t.Fatalf("replaced session body = %s", body)
	}
}

func TestDefaultCORSOriginsIncludeTauriWindows(t *testing.T) {
	t.Setenv("CORS_ORIGIN", "")

	cfg := loadConfig()
	if !aOriginAllowed(cfg, "http://tauri.localhost") {
		t.Fatal("default CORS origins do not allow the Windows Tauri webview")
	}
}
