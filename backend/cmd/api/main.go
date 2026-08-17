package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"pes8-platform/backend/internal/platformdb"
)

type config struct {
	port, mysqlDSN, soccerMySQLDSN, redisAddr, redisPassword string
	jwtSecret, jwtAudience, soccerAuthURL                    string
	corsOrigins                                              map[string]bool
	openVPNClientHost                                        string
	openVPNInternalSecret                                    string
	openVPNClientPortBase                                    int
	openVPNRoomPorts                                         map[int64]int
	noTapRelayHost                                           string
	noTapRelayPort                                           int
	noTapRelayToken                                          string
	noTapIceStunHost                                         string
	noTapIceStunPort                                         int
}

type app struct {
	db              *sql.DB
	redis           *redis.Client
	config          config
	logger          *slog.Logger
	http            *http.Client
	upgrader        websocket.Upgrader
	validateSession func(context.Context, int64, string) (bool, error)
}

type claims struct {
	UserID    int64  `json:"uid"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

type user struct {
	ID                      int64     `json:"id"`
	SoccerUserID            int64     `json:"-"`
	Username                string    `json:"username"`
	Nickname                string    `json:"nickname"`
	PlatformAccessExpiresAt time.Time `json:"-"`
	SessionID               string    `json:"-"`
}

type soccerAuthResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *struct {
		User struct {
			ID                      int64  `json:"id"`
			Username                string `json:"username"`
			Nickname                string `json:"nickname"`
			Status                  int    `json:"status"`
			PlatformAccessExpiresAt string `json:"platform_access_expires_at"`
		} `json:"user"`
	} `json:"data"`
}

var (
	errSoccerAccountDisabled = errors.New("soccer account disabled")
	errSoccerPlatformExpired = errors.New("soccer platform access expired")
	errSoccerRateLimited     = errors.New("soccer login rate limited")
)

type room struct {
	ID         int64  `json:"id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Region     string `json:"region"`
	SubnetCIDR string `json:"subnet_cidr"`
	Capacity   int    `json:"capacity"`
	Members    int    `json:"members"`
	Status     string `json:"status"`
}

type roomMember struct {
	UserID         int64  `json:"user_id"`
	Username       string `json:"username"`
	Nickname       string `json:"nickname"`
	VirtualIP      string `json:"virtual_ip"`
	RealIP         string `json:"real_ip,omitempty"`
	IsSelf         bool   `json:"is_self"`
	IceDescription string `json:"ice_description,omitempty"`
	IceState       string `json:"ice_state"`
}

type lease struct {
	RoomID     int64     `json:"room_id"`
	VirtualIP  string    `json:"virtual_ip"`
	Username   string    `json:"username"`
	Password   string    `json:"password,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
	SubnetCIDR string    `json:"subnet_cidr"`
	Community  string    `json:"community"`
	ServerHost string    `json:"server_host"`
	ServerPort int       `json:"server_port"`
}

// noTapLease deliberately has a separate response type from the TAP lease.
// The two clients use different data planes and must not accidentally consume
// each other's connection metadata.
type noTapLease struct {
	RoomID      int64     `json:"room_id"`
	VirtualIP   string    `json:"virtual_ip"`
	LogicalIP   string    `json:"logical_ip"`
	Username    string    `json:"username"`
	ExpiresAt   time.Time `json:"expires_at"`
	SubnetCIDR  string    `json:"subnet_cidr"`
	Community   string    `json:"community"`
	RelayHost   string    `json:"relay_host"`
	RelayPort   int       `json:"relay_port"`
	RelayToken  string    `json:"relay_token"`
	IceStunHost string    `json:"ice_stun_host"`
	IceStunPort int       `json:"ice_stun_port"`
}

type noTapPeerProbe struct {
	ID                   int64     `json:"id"`
	RequesterUserID      int64     `json:"requester_user_id"`
	TargetUserID         int64     `json:"target_user_id"`
	Purpose              string    `json:"purpose"`
	SessionKey           string    `json:"session_key,omitempty"`
	RequesterDescription string    `json:"requester_description,omitempty"`
	TargetDescription    string    `json:"target_description,omitempty"`
	ExpiresAt            time.Time `json:"expires_at"`
}

type openVPNLeaseSyncRequest struct {
	RoomID    int64  `json:"room_id"`
	Username  string `json:"username"`
	VirtualIP string `json:"virtual_ip"`
	Connected bool   `json:"connected"`
}

const (
	jwtIssuer         = "pes8-platform"
	defaultCORSOrigin = "http://localhost:1420,http://localhost:5173,http://tauri.localhost,https://tauri.localhost,tauri://localhost"
	leaseTTL          = 30 * time.Minute
)

func main() {
	cfg := loadConfig()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	db, err := sql.Open("mysql", cfg.mysqlDSN)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	if err := platformdb.Migrate(ctx, db); err != nil {
		logger.Error("migrate platform database", "error", err)
		os.Exit(1)
	}
	if cfg.soccerMySQLDSN != "" {
		soccerDB, err := sql.Open("mysql", cfg.soccerMySQLDSN)
		if err != nil {
			logger.Error("open soccer database", "error", err)
			os.Exit(1)
		}
		defer soccerDB.Close()
		count, err := syncSoccerUsers(ctx, db, soccerDB)
		if err != nil {
			logger.Error("sync soccer users", "error", err)
			os.Exit(1)
		}
		logger.Info("synced soccer users", "count", count)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.redisAddr, Password: cfg.redisPassword})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Error("connect redis", "error", err)
		os.Exit(1)
	}
	a := &app{db: db, redis: redisClient, config: cfg, logger: logger, http: &http.Client{Timeout: 8 * time.Second},
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return aOriginAllowed(cfg, r.Header.Get("Origin")) }},
	}
	go a.runLeaseReaper(context.Background())
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer, a.requestLogger, a.cors)
	r.Get("/healthz", a.health)
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", a.login)
		r.Post("/internal/openvpn/lease", a.syncOpenVPNLease)
		r.Group(func(r chi.Router) {
			r.Use(a.auth)
			r.Post("/auth/logout", a.logout)
			r.Get("/me", a.me)
			r.Get("/me/room-session", a.roomSession)
			r.Get("/rooms", a.listRooms)
			r.Get("/rooms/{roomID}", a.getRoom)
			r.Get("/rooms/{roomID}/members", a.listRoomMembers)
			r.Post("/rooms/{roomID}/join", a.joinRoom)
			r.Post("/rooms/{roomID}/heartbeat", a.heartbeatRoom)
			r.Post("/rooms/{roomID}/leave", a.leaveRoom)
			r.Get("/rooms/{roomID}/events", a.roomEvents)
			// No-TAP has a separate controller and tables. Its room IDs are
			// intentionally independent from the TAP room IDs above.
			r.Route("/notap", func(r chi.Router) {
				r.Get("/me/room-session", a.noTapRoomSession)
				r.Get("/rooms", a.listNoTapRooms)
				r.Get("/rooms/{roomID}", a.getNoTapRoom)
				r.Get("/rooms/{roomID}/members", a.listNoTapRoomMembers)
				r.Post("/rooms/{roomID}/join", a.joinNoTapRoom)
				r.Post("/rooms/{roomID}/heartbeat", a.heartbeatNoTapRoom)
				r.Post("/rooms/{roomID}/leave", a.leaveNoTapRoom)
				r.Post("/rooms/{roomID}/ice", a.publishNoTapICE)
				r.Get("/rooms/{roomID}/peer-probes/incoming", a.listIncomingNoTapPeerProbes)
				r.Post("/rooms/{roomID}/peer-probes", a.createNoTapPeerProbe)
				r.Get("/rooms/{roomID}/peer-probes/{probeID}", a.getNoTapPeerProbe)
				r.Post("/rooms/{roomID}/peer-probes/{probeID}/answer", a.answerNoTapPeerProbe)
			})
		})
	})
	address := ":" + cfg.port
	logger.Info("api listening", "address", address)
	if err := http.ListenAndServe(address, r); err != nil {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	getenv := func(key, fallback string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return fallback
	}
	origins := map[string]bool{}
	for _, origin := range strings.Split(getenv("CORS_ORIGIN", defaultCORSOrigin), ",") {
		origins[strings.TrimSpace(origin)] = true
	}
	openVPNClientHost := getenv("N2N_CLIENT_HOST", getenv("OPENVPN_CLIENT_HOST", "pending-n2n-host"))
	return config{
		port: getenv("API_PORT", "8080"), mysqlDSN: getenv("MYSQL_DSN", "pes8:pes8-dev-password@tcp(localhost:3306)/pes8_platform?parseTime=true&charset=utf8mb4&loc=Local"),
		soccerMySQLDSN: getenv("SOCCER_MYSQL_DSN", ""),
		redisAddr:      getenv("REDIS_ADDR", "localhost:6379"), redisPassword: getenv("REDIS_PASSWORD", "redis-dev-password"),
		jwtSecret: getenv("JWT_SECRET", "local-development-secret-change-before-production"), jwtAudience: getenv("JWT_AUDIENCE", "we8-platform:"+openVPNClientHost), corsOrigins: origins,
		soccerAuthURL:     getenv("SOCCER_AUTH_URL", "http://localhost/api/v1/auth/platform-login"),
		openVPNClientHost: openVPNClientHost, openVPNInternalSecret: getenv("OPENVPN_INTERNAL_SECRET", ""), openVPNClientPortBase: envInt("N2N_CLIENT_PORT", envInt("N2N_CLIENT_PORT_BASE", envInt("OPENVPN_CLIENT_PORT_BASE", 22222))), openVPNRoomPorts: parseRoomPorts(getenv("N2N_ROOM_PORTS", getenv("OPENVPN_ROOM_PORTS", ""))),
		noTapRelayHost: getenv("WEL_NOTAP_RELAY_HOST", openVPNClientHost), noTapRelayPort: envInt("WEL_NOTAP_RELAY_PORT", 22333), noTapRelayToken: getenv("WEL_NOTAP_RELAY_TOKEN", getenv("WEL_NOTAP_TOKEN", "")),
		noTapIceStunHost: getenv("WEL_NOTAP_ICE_STUN_HOST", "stun.l.google.com"), noTapIceStunPort: envInt("WEL_NOTAP_ICE_STUN_PORT", 19302),
	}
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 1 || value > 65535 {
		return fallback
	}
	return value
}

func parseRoomPorts(raw string) map[int64]int {
	ports := make(map[int64]int)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 {
			continue
		}
		roomID, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil || roomID < 1 {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		ports[roomID] = port
	}
	return ports
}

func (a *app) roomServerPort(roomID int64) int {
	if port, ok := a.config.openVPNRoomPorts[roomID]; ok && port > 0 {
		return port
	}
	if a.config.openVPNClientPortBase > 0 {
		return a.config.openVPNClientPortBase
	}
	return 22222
}

func (a *app) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		a.logger.Info("request", "method", r.Method, "path", r.URL.Path, "request_id", middleware.GetReqID(r.Context()))
	})
}
func (a *app) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if aOriginAllowed(a.config, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func aOriginAllowed(cfg config, origin string) bool { return origin == "" || cfg.corsOrigins[origin] }

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		respondError(w, http.StatusServiceUnavailable, "database is unavailable")
		return
	}
	if err := a.redis.Ping(r.Context()).Err(); err != nil {
		respondError(w, http.StatusServiceUnavailable, "redis is unavailable")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || input.Password == "" {
		respondError(w, http.StatusBadRequest, "请输入账号和密码")
		return
	}

	u, rejected, err := a.authenticateWithSoccer(r.Context(), input.Username, input.Password)
	if err != nil {
		if errors.Is(err, errSoccerAccountDisabled) {
			respondError(w, http.StatusForbidden, "账号已被禁用")
			return
		}
		if errors.Is(err, errSoccerPlatformExpired) {
			respondError(w, http.StatusForbidden, "平台使用权限已到期，请联系管理员")
			return
		}
		if errors.Is(err, errSoccerRateLimited) {
			respondError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
			return
		}
		a.logger.Error("soccer authentication", "error", err)
		respondError(w, http.StatusBadGateway, "账号服务暂时不可用")
		return
	}
	if rejected {
		respondError(w, http.StatusUnauthorized, "账号或密码错误")
		return
	}

	result, err := a.db.ExecContext(r.Context(), `
		INSERT INTO platform_users (soccer_user_id, username_snapshot, nickname_snapshot, last_login_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON DUPLICATE KEY UPDATE
			username_snapshot = VALUES(username_snapshot),
			nickname_snapshot = VALUES(nickname_snapshot),
			last_login_at = CURRENT_TIMESTAMP,
			id = LAST_INSERT_ID(id)`, u.SoccerUserID, u.Username, u.Nickname)
	if err != nil {
		a.logger.Error("sync platform user", "error", err)
		respondError(w, http.StatusInternalServerError, "无法创建平台会话")
		return
	}
	u.ID = resultLastID(result)
	var status string
	if err := a.db.QueryRowContext(r.Context(), "SELECT status FROM platform_users WHERE id = ?", u.ID).Scan(&status); err != nil {
		respondError(w, http.StatusInternalServerError, "无法创建平台会话")
		return
	}
	if status != "active" {
		respondError(w, http.StatusForbidden, "平台账号已被禁用")
		return
	}
	sessionID, err := randomSecret(32)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法创建登录会话")
		return
	}
	if err := a.activateSession(r.Context(), u.ID, sessionID); err != nil {
		a.logger.Error("activate login session", "user_id", u.ID, "error", err)
		respondError(w, http.StatusInternalServerError, "无法创建登录会话")
		return
	}
	u.SessionID = sessionID
	a.respondSession(w, u)
}

func syncSoccerUsers(ctx context.Context, platformDB, soccerDB *sql.DB) (int, error) {
	rows, err := soccerDB.QueryContext(ctx, `
		SELECT
			id,
			username,
			COALESCE(NULLIF(nickname, ''), username) AS nickname,
			CAST(status AS CHAR) AS status
		FROM users`)
	if err != nil {
		return 0, fmt.Errorf("query soccer users: %w", err)
	}
	defer rows.Close()

	tx, err := platformDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin platform user sync: %w", err)
	}
	defer tx.Rollback()

	count := 0
	for rows.Next() {
		var soccerUserID int64
		var username, nickname, status string
		if err := rows.Scan(&soccerUserID, &username, &nickname, &status); err != nil {
			return 0, fmt.Errorf("scan soccer user: %w", err)
		}
		platformStatus := "disabled"
		if status == "1" || strings.EqualFold(status, "active") {
			platformStatus = "active"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO platform_users (soccer_user_id, username_snapshot, nickname_snapshot, status)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				username_snapshot = VALUES(username_snapshot),
				nickname_snapshot = VALUES(nickname_snapshot),
				status = VALUES(status)`, soccerUserID, username, nickname, platformStatus); err != nil {
			return 0, fmt.Errorf("upsert platform user %d: %w", soccerUserID, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read soccer users: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit platform user sync: %w", err)
	}
	return count, nil
}

func (a *app) authenticateWithSoccer(ctx context.Context, username, password string) (user, bool, error) {
	payload, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return user{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.soccerAuthURL, strings.NewReader(string(payload)))
	if err != nil {
		return user{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := a.http.Do(req)
	if err != nil {
		return user{}, false, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return user{}, false, err
	}
	var auth soccerAuthResponse
	if err := json.Unmarshal(body, &auth); err != nil {
		return user{}, false, fmt.Errorf("decode soccer auth response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusUnprocessableEntity {
		return user{}, true, nil
	}
	if response.StatusCode == http.StatusForbidden && auth.Code == 1008 {
		return user{}, false, errSoccerPlatformExpired
	}
	if response.StatusCode == http.StatusForbidden {
		return user{}, false, errSoccerAccountDisabled
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return user{}, false, errSoccerRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return user{}, false, fmt.Errorf("unexpected soccer auth response: status=%d code=%d", response.StatusCode, auth.Code)
	}
	if auth.Code != 0 {
		return user{}, true, nil
	}
	if auth.Data == nil || auth.Data.User.ID == 0 || auth.Data.User.Status != 1 {
		return user{}, false, fmt.Errorf("soccer auth response has no active user")
	}
	platformAccessExpiresAt, err := time.Parse(time.RFC3339, auth.Data.User.PlatformAccessExpiresAt)
	if err != nil || !platformAccessExpiresAt.After(time.Now()) {
		return user{}, false, fmt.Errorf("soccer auth response has invalid platform access expiry")
	}
	return user{
		SoccerUserID:            auth.Data.User.ID,
		Username:                auth.Data.User.Username,
		Nickname:                auth.Data.User.Nickname,
		PlatformAccessExpiresAt: platformAccessExpiresAt,
	}, false, nil
}

func (a *app) respondSession(w http.ResponseWriter, u user) {
	token, err := a.issueToken(u)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法创建登录会话")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"token": token, "user": u})
}
func (a *app) issueToken(u user) (string, error) {
	if u.SessionID == "" {
		return "", errors.New("missing login session ID")
	}
	issuedAt := time.Now()
	expiresAt := issuedAt.Add(24 * time.Hour)
	if !u.PlatformAccessExpiresAt.IsZero() && u.PlatformAccessExpiresAt.Before(expiresAt) {
		expiresAt = u.PlatformAccessExpiresAt
	}
	if !expiresAt.After(issuedAt) {
		return "", errSoccerPlatformExpired
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims{UserID: u.ID, SessionID: u.SessionID, RegisteredClaims: jwt.RegisteredClaims{Issuer: jwtIssuer, Subject: strconv.FormatInt(u.ID, 10), Audience: jwt.ClaimStrings{a.config.jwtAudience}, ExpiresAt: jwt.NewNumericDate(expiresAt), IssuedAt: jwt.NewNumericDate(issuedAt)}}).SignedString([]byte(a.config.jwtSecret))
}

func (a *app) activateSession(ctx context.Context, userID int64, sessionID string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM platform_users WHERE id = ? FOR UPDATE", userID).Scan(&status); err != nil {
		return err
	}
	if status != "active" {
		return errors.New("platform account is not active")
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM room_ip_leases WHERE user_id = ?", userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM no_tap_room_leases WHERE user_id = ?", userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE platform_users SET active_session_id = ? WHERE id = ?", sessionID, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (a *app) endSession(ctx context.Context, userID int64, sessionID string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var activeSession sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT active_session_id FROM platform_users WHERE id = ? FOR UPDATE", userID).Scan(&activeSession); err != nil {
		return err
	}
	if !activeSession.Valid || activeSession.String != sessionID {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM room_ip_leases WHERE user_id = ? AND session_id = ?", userID, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM no_tap_room_leases WHERE user_id = ? AND session_id = ?", userID, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE platform_users SET active_session_id = NULL WHERE id = ? AND active_session_id = ?", userID, sessionID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (a *app) isSessionCurrent(ctx context.Context, userID int64, sessionID string) (bool, error) {
	if a.validateSession != nil {
		return a.validateSession(ctx, userID, sessionID)
	}
	var activeSession sql.NullString
	err := a.db.QueryRowContext(ctx, "SELECT active_session_id FROM platform_users WHERE id = ? AND status = 'active'", userID).Scan(&activeSession)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return activeSession.Valid && activeSession.String == sessionID, nil
}

func (a *app) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		token, err := jwt.ParseWithClaims(raw, &claims{}, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(a.config.jwtSecret), nil
		}, jwt.WithIssuer(jwtIssuer), jwt.WithAudience(a.config.jwtAudience), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
		if err != nil || !token.Valid {
			respondErrorCode(w, http.StatusUnauthorized, "AUTH_INVALID", "登录已失效，请重新登录")
			return
		}
		c, ok := token.Claims.(*claims)
		if !ok || c.UserID == 0 || c.SessionID == "" {
			respondErrorCode(w, http.StatusUnauthorized, "AUTH_INVALID", "登录已失效，请重新登录")
			return
		}
		current, err := a.isSessionCurrent(r.Context(), c.UserID, c.SessionID)
		if err != nil {
			a.logger.Error("validate login session", "user_id", c.UserID, "error", err)
			respondError(w, http.StatusServiceUnavailable, "暂时无法验证登录状态")
			return
		}
		if !current {
			respondErrorCode(w, http.StatusUnauthorized, "SESSION_REPLACED", "账号已在其他设备登录")
			return
		}
		ctx := context.WithValue(r.Context(), authIdentityKey{}, authIdentity{UserID: c.UserID, SessionID: c.SessionID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type authIdentity struct {
	UserID    int64
	SessionID string
}

type authIdentityKey struct{}

func currentIdentity(r *http.Request) authIdentity {
	identity, _ := r.Context().Value(authIdentityKey{}).(authIdentity)
	return identity
}

func currentUserID(r *http.Request) int64     { return currentIdentity(r).UserID }
func currentSessionID(r *http.Request) string { return currentIdentity(r).SessionID }

func (a *app) syncOpenVPNLease(w http.ResponseWriter, r *http.Request) {
	if a.config.openVPNInternalSecret == "" || r.Header.Get("X-WEL-OpenVPN-Secret") != a.config.openVPNInternalSecret {
		respondError(w, http.StatusUnauthorized, "OpenVPN 同步密钥无效")
		return
	}
	var request openVPNLeaseSyncRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.RoomID < 1 || request.RoomID > 6 || !validVPNUsername(request.Username) {
		respondError(w, http.StatusBadRequest, "OpenVPN 租约数据无效")
		return
	}

	var (
		result sql.Result
		err    error
	)
	if request.Connected {
		ip := net.ParseIP(request.VirtualIP).To4()
		if ip == nil || !strings.HasPrefix(ip.String(), fmt.Sprintf("10.222.%d.", request.RoomID)) {
			respondError(w, http.StatusBadRequest, "OpenVPN 虚拟地址无效")
			return
		}
		result, err = a.db.ExecContext(r.Context(), `
			UPDATE room_ip_leases
			SET virtual_ip = ?, state = 'connected'
			WHERE room_id = ? AND vpn_username = ? AND released_at IS NULL
				AND credential_expires_at > CURRENT_TIMESTAMP`, ip.String(), request.RoomID, request.Username)
	} else {
		result, err = a.db.ExecContext(r.Context(), `
			DELETE FROM room_ip_leases
			WHERE room_id = ? AND vpn_username = ? AND released_at IS NULL`, request.RoomID, request.Username)
	}
	if err != nil {
		a.logger.Error("sync OpenVPN lease", "room_id", request.RoomID, "username", request.Username, "connected", request.Connected, "error", err)
		respondError(w, http.StatusInternalServerError, "无法同步 OpenVPN 地址")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		respondError(w, http.StatusNotFound, "OpenVPN 租约不存在")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func validVPNUsername(username string) bool {
	if len(username) < 1 || len(username) > 96 {
		return false
	}
	for _, char := range username {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		value := strings.TrimSpace(r.Header.Get(header))
		if value == "" {
			continue
		}
		if header == "X-Forwarded-For" {
			value = strings.TrimSpace(strings.Split(value, ",")[0])
		}
		if ip := net.ParseIP(value); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(r.RemoteAddr); ip != nil {
		return ip.String()
	}
	return ""
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.endSession(r.Context(), currentUserID(r), currentSessionID(r)); err != nil {
		a.logger.Error("end login session", "user_id", currentUserID(r), "error", err)
		respondError(w, http.StatusInternalServerError, "无法退出登录")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *app) me(w http.ResponseWriter, r *http.Request) {
	var u user
	err := a.db.QueryRowContext(r.Context(), "SELECT id, soccer_user_id, username_snapshot, nickname_snapshot FROM platform_users WHERE id = ? AND status = 'active'", currentUserID(r)).Scan(&u.ID, &u.SoccerUserID, &u.Username, &u.Nickname)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "账号不可用")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"user": u})
}
func (a *app) roomSession(w http.ResponseWriter, r *http.Request) {
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取房间会话")
		return
	}
	defer tx.Rollback()

	current := lease{}
	var (
		virtualIP      sql.NullString
		code           string
		ipStart, ipEnd string
	)
	query := "SELECT l.room_id, l.virtual_ip, l.vpn_username, r.subnet_cidr, r.code, r.ip_start, r.ip_end, l.credential_expires_at FROM room_ip_leases l INNER JOIN rooms r ON r.id = l.room_id WHERE l.user_id = ? AND l.session_id = ? AND l.released_at IS NULL ORDER BY l.id DESC LIMIT 1 FOR UPDATE"
	err = tx.QueryRowContext(r.Context(), query, currentUserID(r), currentSessionID(r)).Scan(&current.RoomID, &virtualIP, &current.Username, &current.SubnetCIDR, &code, &ipStart, &ipEnd, &current.ExpiresAt)
	if err == sql.ErrNoRows {
		respondJSON(w, http.StatusOK, map[string]any{"lease": nil})
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取房间会话")
		return
	}
	current.VirtualIP = nullableString(virtualIP)
	current.Community = roomCommunity(code, current.RoomID)
	current.ServerHost, current.ServerPort = a.config.openVPNClientHost, a.roomServerPort(current.RoomID)
	if current.VirtualIP == "" {
		assignedIP, assignErr := nextFreeIP(r.Context(), tx, current.RoomID, ipStart, ipEnd)
		if assignErr != nil {
			respondError(w, http.StatusConflict, "房间虚拟地址已耗尽")
			return
		}
		if _, assignErr = tx.ExecContext(r.Context(), "UPDATE room_ip_leases SET virtual_ip = ?, state = 'connected' WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL", assignedIP, current.RoomID, currentUserID(r), currentSessionID(r)); assignErr != nil {
			respondError(w, http.StatusInternalServerError, "无法读取房间会话")
			return
		}
		current.VirtualIP = assignedIP
	}
	if err := tx.Commit(); err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取房间会话")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"lease": current})
}

const roomSelect = "SELECT r.id, r.code, r.name, r.region, r.subnet_cidr, r.capacity, r.status, COUNT(l.id) AS members FROM rooms r LEFT JOIN room_ip_leases l ON l.room_id = r.id AND l.released_at IS NULL AND l.credential_expires_at > CURRENT_TIMESTAMP GROUP BY r.id ORDER BY r.sort_order, r.id"

func (a *app) listRooms(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), roomSelect)
	if err != nil {
		respondError(w, 500, "无法读取房间")
		return
	}
	defer rows.Close()
	rooms := make([]room, 0)
	for rows.Next() {
		var item room
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Region, &item.SubnetCIDR, &item.Capacity, &item.Status, &item.Members); err != nil {
			respondError(w, 500, "无法读取房间")
			return
		}
		rooms = append(rooms, item)
	}
	respondJSON(w, 200, map[string]any{"rooms": rooms})
}
func (a *app) getRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	var item room
	query := "SELECT r.id, r.code, r.name, r.region, r.subnet_cidr, r.capacity, r.status, COUNT(l.id) AS members FROM rooms r LEFT JOIN room_ip_leases l ON l.room_id = r.id AND l.released_at IS NULL AND l.credential_expires_at > CURRENT_TIMESTAMP WHERE r.id = ? GROUP BY r.id"
	err := a.db.QueryRowContext(r.Context(), query, roomID).Scan(&item.ID, &item.Code, &item.Name, &item.Region, &item.SubnetCIDR, &item.Capacity, &item.Status, &item.Members)
	if err == sql.ErrNoRows {
		respondError(w, 404, "房间不存在")
		return
	}
	if err != nil {
		respondError(w, 500, "无法读取房间")
		return
	}
	respondJSON(w, 200, map[string]any{"room": item})
}

func (a *app) listRoomMembers(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}

	var joined bool
	err := a.db.QueryRowContext(r.Context(), `
		SELECT EXISTS(
			SELECT 1
			FROM room_ip_leases
			WHERE room_id = ? AND user_id = ? AND session_id = ?
				AND released_at IS NULL AND credential_expires_at > NOW()
		)`, roomID, currentUserID(r), currentSessionID(r)).Scan(&joined)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取房间成员")
		return
	}
	if !joined {
		respondError(w, http.StatusForbidden, "请先进入该房间")
		return
	}

	rows, err := a.db.QueryContext(r.Context(), `
		SELECT p.id, p.username_snapshot, p.nickname_snapshot, l.virtual_ip, COALESCE(l.real_ip, ''), l.user_id = ? AS is_self
		FROM room_ip_leases l
		INNER JOIN platform_users p ON p.id = l.user_id
		WHERE l.room_id = ? AND l.released_at IS NULL
			AND l.credential_expires_at > NOW() AND p.status = 'active'
		ORDER BY l.user_id = ? DESC, p.nickname_snapshot, p.username_snapshot`, currentUserID(r), roomID, currentUserID(r))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取房间成员")
		return
	}
	defer rows.Close()

	members := make([]roomMember, 0)
	for rows.Next() {
		var (
			member    roomMember
			virtualIP sql.NullString
			realIP    sql.NullString
		)
		if err := rows.Scan(&member.UserID, &member.Username, &member.Nickname, &virtualIP, &realIP, &member.IsSelf); err != nil {
			respondError(w, http.StatusInternalServerError, "无法读取房间成员")
			return
		}
		member.VirtualIP = nullableString(virtualIP)
		member.RealIP = nullableString(realIP)
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取房间成员")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"members": members})
}

func sessionCurrentForUpdate(ctx context.Context, tx *sql.Tx, userID int64, sessionID string) (bool, error) {
	var activeSession sql.NullString
	var status string
	err := tx.QueryRowContext(ctx, "SELECT active_session_id, status FROM platform_users WHERE id = ? FOR UPDATE", userID).Scan(&activeSession, &status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "active" && activeSession.Valid && activeSession.String == sessionID, nil
}

func (a *app) joinRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID := currentUserID(r)
	requestIP := clientIP(r)
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		respondError(w, 500, "无法进入房间")
		return
	}
	defer tx.Rollback()
	current, err := sessionCurrentForUpdate(r.Context(), tx, userID, currentSessionID(r))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法验证登录状态")
		return
	}
	if !current {
		respondErrorCode(w, http.StatusUnauthorized, "SESSION_REPLACED", "账号已在其他设备登录")
		return
	}
	var code, subnet, ipStart, ipEnd, status string
	var capacity int
	err = tx.QueryRowContext(r.Context(), "SELECT code, subnet_cidr, ip_start, ip_end, status, capacity FROM rooms WHERE id = ? FOR UPDATE", roomID).Scan(&code, &subnet, &ipStart, &ipEnd, &status, &capacity)
	if err == sql.ErrNoRows {
		respondError(w, 404, "房间不存在")
		return
	}
	if err != nil {
		respondError(w, 500, "无法进入房间")
		return
	}
	if status != "open" {
		respondError(w, 409, "房间暂不可进入")
		return
	}
	var (
		existingIP       sql.NullString
		existingUsername string
	)
	err = tx.QueryRowContext(r.Context(), "SELECT virtual_ip, vpn_username FROM room_ip_leases WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL ORDER BY id DESC LIMIT 1 FOR UPDATE", roomID, userID, currentSessionID(r)).Scan(&existingIP, &existingUsername)
	if err == nil {
		expiresAt := time.Now().Add(leaseTTL)
		assignedIP := nullableString(existingIP)
		if assignedIP == "" {
			assignedIP, err = nextFreeIP(r.Context(), tx, roomID, ipStart, ipEnd)
			if err != nil {
				respondError(w, http.StatusConflict, "房间虚拟地址已耗尽")
				return
			}
		}
		if _, updateErr := tx.ExecContext(r.Context(), "UPDATE room_ip_leases SET virtual_ip = ?, credential_expires_at = ?, real_ip = ?, state = 'connected' WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL", assignedIP, expiresAt, requestIP, roomID, userID, currentSessionID(r)); updateErr != nil {
			respondError(w, 500, "无法刷新连接凭据")
			return
		}
		if commitErr := tx.Commit(); commitErr != nil {
			respondError(w, 500, "无法进入房间")
			return
		}
		respondJSON(w, 200, map[string]any{"lease": lease{RoomID: roomID, VirtualIP: assignedIP, Username: existingUsername, SubnetCIDR: subnet, Community: roomCommunity(code, roomID), ExpiresAt: expiresAt, ServerHost: a.config.openVPNClientHost, ServerPort: a.roomServerPort(roomID)}})
		return
	}
	if err != sql.ErrNoRows {
		respondError(w, 500, "无法进入房间")
		return
	}
	var members int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM room_ip_leases WHERE room_id = ? AND released_at IS NULL", roomID).Scan(&members); err != nil {
		respondError(w, 500, "无法进入房间")
		return
	}
	if members >= capacity {
		respondError(w, 409, "房间已满")
		return
	}
	username := fmt.Sprintf("room-%d-user-%d-%d", roomID, userID, time.Now().Unix())
	expiresAt := time.Now().Add(leaseTTL)
	assignedIP, err := nextFreeIP(r.Context(), tx, roomID, ipStart, ipEnd)
	if err != nil {
		respondError(w, http.StatusConflict, "房间虚拟地址已耗尽")
		return
	}
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO room_ip_leases (room_id, user_id, session_id, virtual_ip, vpn_username, real_ip, credential_expires_at, state) VALUES (?, ?, ?, ?, ?, ?, ?, 'connected')", roomID, userID, currentSessionID(r), assignedIP, username, requestIP, expiresAt); err != nil {
		respondError(w, 500, "无法分配虚拟地址")
		return
	}
	if err := tx.Commit(); err != nil {
		respondError(w, 500, "无法进入房间")
		return
	}
	respondJSON(w, 200, map[string]any{"lease": lease{RoomID: roomID, VirtualIP: assignedIP, Username: username, SubnetCIDR: subnet, Community: roomCommunity(code, roomID), ExpiresAt: expiresAt, ServerHost: a.config.openVPNClientHost, ServerPort: a.roomServerPort(roomID)}})
}

func (a *app) heartbeatRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID := currentUserID(r)
	sessionID := currentSessionID(r)
	requestIP := clientIP(r)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法续期房间连接")
		return
	}
	defer tx.Rollback()
	current, err := sessionCurrentForUpdate(r.Context(), tx, userID, sessionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法验证登录状态")
		return
	}
	if !current {
		respondErrorCode(w, http.StatusUnauthorized, "SESSION_REPLACED", "账号已在其他设备登录")
		return
	}
	var leaseID int64
	err = tx.QueryRowContext(r.Context(), `
		SELECT l.id
		FROM room_ip_leases l
		INNER JOIN rooms ON rooms.id = l.room_id
		WHERE l.room_id = ? AND l.user_id = ? AND l.session_id = ? AND l.released_at IS NULL
			AND l.credential_expires_at > CURRENT_TIMESTAMP
		ORDER BY l.id DESC
		LIMIT 1
		FOR UPDATE`, roomID, userID, sessionID).Scan(&leaseID)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusConflict, "房间连接已过期，请重新进入")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法续期房间连接")
		return
	}

	expiresAt := time.Now().Add(leaseTTL)
	result, err := tx.ExecContext(r.Context(), `
		UPDATE room_ip_leases
		SET credential_expires_at = ?, real_ip = ?
		WHERE id = ? AND room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL
			AND credential_expires_at > CURRENT_TIMESTAMP`, expiresAt, requestIP, leaseID, roomID, userID, sessionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法续期房间连接")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		respondError(w, http.StatusConflict, "房间连接已结束，请重新进入")
		return
	}
	if err := tx.Commit(); err != nil {
		respondError(w, http.StatusInternalServerError, "无法续期房间连接")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"expires_at": expiresAt})
}

func (a *app) leaveRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID := currentUserID(r)
	sessionID := currentSessionID(r)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法退出房间")
		return
	}
	defer tx.Rollback()
	current, err := sessionCurrentForUpdate(r.Context(), tx, userID, sessionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法验证登录状态")
		return
	}
	if !current {
		respondErrorCode(w, http.StatusUnauthorized, "SESSION_REPLACED", "账号已在其他设备登录")
		return
	}
	var username string
	err = tx.QueryRowContext(r.Context(), "SELECT l.vpn_username FROM room_ip_leases l INNER JOIN rooms r ON r.id = l.room_id WHERE l.room_id = ? AND l.user_id = ? AND l.session_id = ? AND l.released_at IS NULL ORDER BY l.id DESC LIMIT 1 FOR UPDATE", roomID, userID, sessionID).Scan(&username)
	if err == sql.ErrNoRows {
		respondError(w, 404, "你不在该房间内")
		return
	}
	if err != nil {
		respondError(w, 500, "无法退出房间")
		return
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM room_ip_leases WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL", roomID, userID, sessionID); err != nil {
		respondError(w, 500, "无法退出房间")
		return
	}
	if err := tx.Commit(); err != nil {
		respondError(w, 500, "无法退出房间")
		return
	}
	respondJSON(w, 200, map[string]bool{"ok": true})
}

func (a *app) roomEvents(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"type": "room.connected", "room_id": roomID})
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if err := conn.WriteJSON(map[string]any{"type": "room.ping", "at": time.Now().UTC()}); err != nil {
			return
		}
	}
}

func (a *app) runLeaseReaper(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		a.reapExpiredLeases(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *app) reapExpiredLeases(ctx context.Context) {
	rows, err := a.db.QueryContext(ctx, "SELECT l.id FROM room_ip_leases l INNER JOIN rooms r ON r.id = l.room_id WHERE l.released_at IS NULL AND l.credential_expires_at <= NOW() LIMIT 100")
	if err != nil {
		a.logger.Error("find expired leases", "error", err)
		return
	}
	type expiredLease struct{ id int64 }
	expired := make([]expiredLease, 0)
	for rows.Next() {
		var item expiredLease
		if err := rows.Scan(&item.id); err == nil {
			expired = append(expired, item)
		}
	}
	rows.Close()
	for _, item := range expired {
		result, err := a.db.ExecContext(ctx, "DELETE FROM room_ip_leases WHERE id = ? AND credential_expires_at <= NOW()", item.id)
		if err != nil {
			a.logger.Error("delete expired lease", "lease_id", item.id, "error", err)
			continue
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			continue
		}
	}
	if _, err := a.db.ExecContext(ctx, "DELETE FROM no_tap_room_leases WHERE released_at IS NULL AND credential_expires_at <= UTC_TIMESTAMP()"); err != nil {
		a.logger.Error("delete expired no-TAP leases", "error", err)
	}
}

func roomIDFromRequest(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "roomID"), 10, 64)
	if err != nil || id < 1 {
		respondError(w, 400, "无效房间编号")
		return 0, false
	}
	return id, true
}

func nullableString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func nextFreeIP(ctx context.Context, tx *sql.Tx, roomID int64, start, end string) (string, error) {
	from, to := net.ParseIP(start).To4(), net.ParseIP(end).To4()
	if from == nil || to == nil {
		return "", errors.New("invalid room ip range")
	}
	usedRows, err := tx.QueryContext(ctx, "SELECT virtual_ip FROM room_ip_leases WHERE room_id = ? AND released_at IS NULL FOR UPDATE", roomID)
	if err != nil {
		return "", err
	}
	defer usedRows.Close()
	used := map[string]bool{}
	for usedRows.Next() {
		var ip sql.NullString
		if err := usedRows.Scan(&ip); err != nil {
			return "", err
		}
		if ip.Valid && ip.String != "" {
			used[ip.String] = true
		}
	}
	for current := append(net.IP(nil), from...); ; incrementIPv4(current) {
		candidate := current.String()
		if !used[candidate] {
			return candidate, nil
		}
		if current.Equal(to) {
			break
		}
	}
	return "", errors.New("no free ip")
}

func roomCommunity(code string, roomID int64) string {
	if strings.TrimSpace(code) == "" {
		return fmt.Sprintf("wel-room-%d", roomID)
	}
	return code
}

func incrementIPv4(ip net.IP) {
	for index := len(ip) - 1; index >= 0; index-- {
		ip[index]++
		if ip[index] != 0 {
			break
		}
	}
}
func randomSecret(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
func resultLastID(result sql.Result) int64 { id, _ := result.LastInsertId(); return id }
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		respondError(w, 400, "请求数据格式不正确")
		return false
	}
	return true
}
func respondJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func respondErrorCode(w http.ResponseWriter, status int, code, message string) {
	respondJSON(w, status, map[string]string{"code": code, "error": message})
}
