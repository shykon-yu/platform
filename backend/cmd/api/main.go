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
	"pes8-platform/backend/internal/vpn"
)

type config struct {
	port, mysqlDSN, soccerMySQLDSN, redisAddr, redisPassword string
	jwtSecret, soccerAuthURL                                 string
	corsOrigins                                              map[string]bool
	softEtherMode, vpncmdPath, softEtherAdminEndpoint        string
	softEtherAdminPassword, softEtherClientHost              string
	softEtherClientPort                                      int
}

type app struct {
	db       *sql.DB
	redis    *redis.Client
	config   config
	logger   *slog.Logger
	http     *http.Client
	upgrader websocket.Upgrader
	vpn      vpn.Provisioner
}

type claims struct {
	UserID int64 `json:"uid"`
	jwt.RegisteredClaims
}

type user struct {
	ID           int64  `json:"id"`
	SoccerUserID int64  `json:"-"`
	Username     string `json:"username"`
	Nickname     string `json:"nickname"`
}

type soccerAuthResponse struct {
	Code int `json:"code"`
	Data *struct {
		User struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Nickname string `json:"nickname"`
			Status   int    `json:"status"`
		} `json:"user"`
	} `json:"data"`
}

var (
	errSoccerAccountDisabled = errors.New("soccer account disabled")
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

type lease struct {
	RoomID     int64     `json:"room_id"`
	VirtualIP  string    `json:"virtual_ip"`
	Username   string    `json:"username"`
	Password   string    `json:"password,omitempty"`
	HubName    string    `json:"hub_name"`
	ExpiresAt  time.Time `json:"expires_at"`
	SubnetCIDR string    `json:"subnet_cidr"`
	ServerHost string    `json:"server_host"`
	ServerPort int       `json:"server_port"`
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
	vpnProvisioner, err := vpn.New(vpn.Config{Mode: cfg.softEtherMode, VPNCmdPath: cfg.vpncmdPath, AdminEndpoint: cfg.softEtherAdminEndpoint, AdminPassword: cfg.softEtherAdminPassword, Logger: logger})
	if err != nil {
		logger.Error("configure SoftEther", "error", err)
		os.Exit(1)
	}

	a := &app{db: db, redis: redisClient, config: cfg, logger: logger, vpn: vpnProvisioner, http: &http.Client{Timeout: 8 * time.Second},
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return aOriginAllowed(cfg, r.Header.Get("Origin")) }},
	}
	go a.runLeaseReaper(context.Background())
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer, a.requestLogger, a.cors)
	r.Get("/healthz", a.health)
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", a.login)
		r.Group(func(r chi.Router) {
			r.Use(a.auth)
			r.Get("/me", a.me)
			r.Get("/me/room-session", a.roomSession)
			r.Get("/rooms", a.listRooms)
			r.Get("/rooms/{roomID}", a.getRoom)
			r.Post("/rooms/{roomID}/join", a.joinRoom)
			r.Post("/rooms/{roomID}/heartbeat", a.heartbeatRoom)
			r.Post("/rooms/{roomID}/leave", a.leaveRoom)
			r.Get("/rooms/{roomID}/events", a.roomEvents)
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
	return config{
		port: getenv("API_PORT", "8080"), mysqlDSN: getenv("MYSQL_DSN", "pes8:pes8-dev-password@tcp(localhost:3306)/pes8_platform?parseTime=true&charset=utf8mb4&loc=Local"),
		soccerMySQLDSN: getenv("SOCCER_MYSQL_DSN", ""),
		redisAddr:      getenv("REDIS_ADDR", "localhost:6379"), redisPassword: getenv("REDIS_PASSWORD", "redis-dev-password"),
		jwtSecret: getenv("JWT_SECRET", "local-development-secret-change-before-production"), corsOrigins: origins,
		soccerAuthURL: getenv("SOCCER_AUTH_URL", "http://localhost/api/v1/auth/login"),
		softEtherMode: getenv("SOFTETHER_MODE", "mock"), vpncmdPath: getenv("SOFTETHER_VPNCMD_PATH", "/usr/local/bin/vpncmd"),
		softEtherAdminEndpoint: getenv("SOFTETHER_ADMIN_ENDPOINT", "localhost:5555"), softEtherAdminPassword: getenv("SOFTETHER_ADMIN_PASSWORD", ""),
		softEtherClientHost: getenv("SOFTETHER_CLIENT_HOST", "pending-softether-host"), softEtherClientPort: envInt("SOFTETHER_CLIENT_PORT", 443),
	}
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 1 || value > 65535 {
		return fallback
	}
	return value
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
	return user{
		SoccerUserID: auth.Data.User.ID,
		Username:     auth.Data.User.Username,
		Nickname:     auth.Data.User.Nickname,
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
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims{UserID: u.ID, RegisteredClaims: jwt.RegisteredClaims{Issuer: jwtIssuer, Subject: strconv.FormatInt(u.ID, 10), ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}).SignedString([]byte(a.config.jwtSecret))
}

func (a *app) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		token, err := jwt.ParseWithClaims(raw, &claims{}, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return []byte(a.config.jwtSecret), nil
		}, jwt.WithIssuer(jwtIssuer), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
		if err != nil || !token.Valid {
			respondError(w, http.StatusUnauthorized, "登录已失效")
			return
		}
		c, ok := token.Claims.(*claims)
		if !ok {
			respondError(w, http.StatusUnauthorized, "登录已失效")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userIDKey{}, c.UserID)))
	})
}

type userIDKey struct{}

func currentUserID(r *http.Request) int64 { id, _ := r.Context().Value(userIDKey{}).(int64); return id }

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
	var current lease
	query := "SELECT l.room_id, l.virtual_ip, l.softether_username, r.hub_name, r.subnet_cidr, l.credential_expires_at FROM room_ip_leases l INNER JOIN rooms r ON r.id = l.room_id WHERE l.user_id = ? AND l.released_at IS NULL ORDER BY l.id DESC LIMIT 1"
	err := a.db.QueryRowContext(r.Context(), query, currentUserID(r)).Scan(&current.RoomID, &current.VirtualIP, &current.Username, &current.HubName, &current.SubnetCIDR, &current.ExpiresAt)
	if err == sql.ErrNoRows {
		respondJSON(w, http.StatusOK, map[string]any{"lease": nil})
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取房间会话")
		return
	}
	current.ServerHost, current.ServerPort = a.config.softEtherClientHost, a.config.softEtherClientPort
	respondJSON(w, http.StatusOK, map[string]any{"lease": current})
}

const roomSelect = "SELECT r.id, r.code, r.name, r.region, r.subnet_cidr, r.capacity, r.status, COUNT(l.id) AS members FROM rooms r LEFT JOIN room_ip_leases l ON l.room_id = r.id AND l.released_at IS NULL GROUP BY r.id ORDER BY r.sort_order, r.id"

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
	query := "SELECT r.id, r.code, r.name, r.region, r.subnet_cidr, r.capacity, r.status, COUNT(l.id) AS members FROM rooms r LEFT JOIN room_ip_leases l ON l.room_id = r.id AND l.released_at IS NULL WHERE r.id = ? GROUP BY r.id"
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

func (a *app) joinRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID := currentUserID(r)
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		respondError(w, 500, "无法进入房间")
		return
	}
	defer tx.Rollback()
	var hub, subnet, start, end, status string
	var capacity int
	err = tx.QueryRowContext(r.Context(), "SELECT hub_name, subnet_cidr, ip_start, ip_end, status, capacity FROM rooms WHERE id = ? FOR UPDATE", roomID).Scan(&hub, &subnet, &start, &end, &status, &capacity)
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
	var members int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM room_ip_leases WHERE room_id = ? AND released_at IS NULL", roomID).Scan(&members); err != nil {
		respondError(w, 500, "无法进入房间")
		return
	}
	if members >= capacity {
		respondError(w, 409, "房间已满")
		return
	}
	var existingIP, existingUsername string
	err = tx.QueryRowContext(r.Context(), "SELECT virtual_ip, softether_username FROM room_ip_leases WHERE room_id = ? AND user_id = ? AND released_at IS NULL ORDER BY id DESC LIMIT 1", roomID, userID).Scan(&existingIP, &existingUsername)
	if err == nil {
		password, secretErr := randomSecret(24)
		if secretErr != nil {
			respondError(w, 500, "无法刷新连接凭据")
			return
		}
		expiresAt := time.Now().Add(leaseTTL)
		if _, updateErr := tx.ExecContext(r.Context(), "UPDATE room_ip_leases SET credential_expires_at = ? WHERE room_id = ? AND user_id = ? AND released_at IS NULL", expiresAt, roomID, userID); updateErr != nil {
			respondError(w, 500, "无法刷新连接凭据")
			return
		}
		if commitErr := tx.Commit(); commitErr != nil {
			respondError(w, 500, "无法进入房间")
			return
		}
		credential := vpn.Credential{Hub: hub, Username: existingUsername, Password: password, ExpiresAt: expiresAt}
		if provisionErr := a.vpn.Provision(r.Context(), credential); provisionErr != nil {
			respondError(w, 502, "无法创建虚拟网络凭据")
			return
		}
		respondJSON(w, 200, map[string]any{"lease": lease{RoomID: roomID, VirtualIP: existingIP, Username: existingUsername, Password: password, HubName: hub, SubnetCIDR: subnet, ExpiresAt: expiresAt, ServerHost: a.config.softEtherClientHost, ServerPort: a.config.softEtherClientPort}})
		return
	}
	if err != sql.ErrNoRows {
		respondError(w, 500, "无法进入房间")
		return
	}
	ip, err := nextFreeIP(r.Context(), tx, roomID, start, end)
	if err != nil {
		respondError(w, 409, "房间地址已用完")
		return
	}
	username := fmt.Sprintf("room-%d-user-%d-%d", roomID, userID, time.Now().Unix())
	password, err := randomSecret(24)
	if err != nil {
		respondError(w, 500, "无法创建连接凭据")
		return
	}
	expiresAt := time.Now().Add(leaseTTL)
	if _, err := tx.ExecContext(r.Context(), "INSERT INTO room_ip_leases (room_id, user_id, virtual_ip, softether_username, credential_expires_at) VALUES (?, ?, ?, ?, ?)", roomID, userID, ip, username, expiresAt); err != nil {
		respondError(w, 500, "无法分配虚拟地址")
		return
	}
	if err := tx.Commit(); err != nil {
		respondError(w, 500, "无法进入房间")
		return
	}
	credential := vpn.Credential{Hub: hub, Username: username, Password: password, ExpiresAt: expiresAt}
	if provisionErr := a.vpn.Provision(r.Context(), credential); provisionErr != nil {
		_, _ = a.db.ExecContext(r.Context(), "DELETE FROM room_ip_leases WHERE room_id = ? AND user_id = ? AND released_at IS NULL", roomID, userID)
		respondError(w, 502, "无法创建虚拟网络凭据")
		return
	}
	respondJSON(w, 200, map[string]any{"lease": lease{RoomID: roomID, VirtualIP: ip, Username: username, Password: password, HubName: hub, SubnetCIDR: subnet, ExpiresAt: expiresAt, ServerHost: a.config.softEtherClientHost, ServerPort: a.config.softEtherClientPort}})
}

func (a *app) heartbeatRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID := currentUserID(r)
	var leaseID int64
	var username, hub string
	err := a.db.QueryRowContext(r.Context(), `
		SELECT l.id, l.softether_username, rooms.hub_name
		FROM room_ip_leases l
		INNER JOIN rooms ON rooms.id = l.room_id
		WHERE l.room_id = ? AND l.user_id = ? AND l.released_at IS NULL
			AND l.credential_expires_at > CURRENT_TIMESTAMP
		ORDER BY l.id DESC
		LIMIT 1`, roomID, userID).Scan(&leaseID, &username, &hub)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusConflict, "房间连接已过期，请重新进入")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法续期房间连接")
		return
	}

	expiresAt := time.Now().Add(leaseTTL)
	if err := a.vpn.Renew(r.Context(), hub, username, expiresAt); err != nil {
		a.logger.Error("renew SoftEther credential", "room_id", roomID, "user_id", userID, "error", err)
		respondError(w, http.StatusBadGateway, "无法续期虚拟网络凭据")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `
		UPDATE room_ip_leases
		SET credential_expires_at = ?
		WHERE id = ? AND room_id = ? AND user_id = ? AND released_at IS NULL
			AND credential_expires_at > CURRENT_TIMESTAMP`, expiresAt, leaseID, roomID, userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法续期房间连接")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		respondError(w, http.StatusConflict, "房间连接已结束，请重新进入")
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
	var username, hub string
	err := a.db.QueryRowContext(r.Context(), "SELECT l.softether_username, r.hub_name FROM room_ip_leases l INNER JOIN rooms r ON r.id = l.room_id WHERE l.room_id = ? AND l.user_id = ? AND l.released_at IS NULL ORDER BY l.id DESC LIMIT 1", roomID, userID).Scan(&username, &hub)
	if err == sql.ErrNoRows {
		respondError(w, 404, "你不在该房间内")
		return
	}
	if err != nil {
		respondError(w, 500, "无法退出房间")
		return
	}
	if _, err = a.db.ExecContext(r.Context(), "DELETE FROM room_ip_leases WHERE room_id = ? AND user_id = ? AND released_at IS NULL", roomID, userID); err != nil {
		respondError(w, 500, "无法退出房间")
		return
	}
	if err := a.vpn.Revoke(r.Context(), hub, username); err != nil {
		a.logger.Error("revoke SoftEther credential", "room_id", roomID, "user_id", userID, "error", err)
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
	rows, err := a.db.QueryContext(ctx, "SELECT l.id, l.softether_username, r.hub_name FROM room_ip_leases l INNER JOIN rooms r ON r.id = l.room_id WHERE l.released_at IS NULL AND l.credential_expires_at <= NOW() LIMIT 100")
	if err != nil {
		a.logger.Error("find expired leases", "error", err)
		return
	}
	type expiredLease struct {
		id            int64
		username, hub string
	}
	expired := make([]expiredLease, 0)
	for rows.Next() {
		var item expiredLease
		if err := rows.Scan(&item.id, &item.username, &item.hub); err == nil {
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
		if err := a.vpn.Revoke(ctx, item.hub, item.username); err != nil {
			a.logger.Error("revoke expired SoftEther credential", "lease_id", item.id, "error", err)
		}
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
		var ip string
		if err := usedRows.Scan(&ip); err != nil {
			return "", err
		}
		used[ip] = true
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
