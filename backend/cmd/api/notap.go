package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// No-TAP controller. This intentionally does not share room_ip_leases with
// the TAP/n2n controller. The route prefix and table names are separate so a
// client from one transport cannot accidentally receive the other transport's
// IP or connection parameters.

const noTapRoomSelect = `
	SELECT r.id, r.code, r.name, r.region, r.subnet_cidr, r.capacity, r.status, r.connection_mode,
		COUNT(l.id) AS members
	FROM no_tap_rooms r
	LEFT JOIN no_tap_room_leases l
		ON l.room_id = r.id AND l.released_at IS NULL AND l.credential_expires_at > UTC_TIMESTAMP()
	WHERE r.id BETWEEN 1 AND 6
	GROUP BY r.id
	ORDER BY r.sort_order, r.id`

func (a *app) noTapLeasePayload(roomID int64, code, subnet, virtualIP, username, connectionMode string, expiresAt time.Time) noTapLease {
	payload := noTapLease{
		RoomID: roomID, VirtualIP: virtualIP, LogicalIP: virtualIP, Username: username,
		ExpiresAt: expiresAt, SubnetCIDR: subnet, ConnectionMode: connectionMode,
	}
	// Keep transport credentials mode-specific. This prevents a TAP client from
	// accidentally consuming ICE/relay metadata and prevents a No-TAP client
	// from falling back to the n2n server fields.
	switch connectionMode {
	case "tap":
		payload.Community = roomCommunity(code, roomID)
		payload.ServerHost = a.config.n2nClientHost
		payload.ServerPort = a.n2nServerPort(roomID)
	default:
		payload.Community = roomCommunity(code, roomID)
		payload.RelayHost = a.config.noTapRelayHost
		payload.RelayPort = a.config.noTapRelayPort
		payload.RelayToken = a.config.noTapRelayToken
		if connectionMode == "direct" {
			payload.IceStunHost = a.config.noTapIceStunHost
			payload.IceStunPort = a.config.noTapIceStunPort
		}
	}
	return payload
}

func (a *app) noTapRoomSession(w http.ResponseWriter, r *http.Request) {
	var current noTapLease
	var (
		code, subnet, connectionMode, virtualIP, username string
	)
	err := a.db.QueryRowContext(r.Context(), `
		SELECT l.room_id, l.virtual_ip, l.relay_username, r.code, r.subnet_cidr, r.connection_mode, l.credential_expires_at
		FROM no_tap_room_leases l
		INNER JOIN no_tap_rooms r ON r.id = l.room_id
		WHERE l.user_id = ? AND l.session_id = ? AND l.released_at IS NULL
			AND l.credential_expires_at > UTC_TIMESTAMP()
		ORDER BY l.id DESC LIMIT 1`, currentUserID(r), currentSessionID(r)).Scan(
		&current.RoomID, &virtualIP, &username, &code, &subnet, &connectionMode, &current.ExpiresAt)
	if err == sql.ErrNoRows {
		respondJSON(w, http.StatusOK, map[string]any{"lease": nil})
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取无网卡房间会话")
		return
	}
	current = a.noTapLeasePayload(current.RoomID, code, subnet, virtualIP, username, connectionMode, current.ExpiresAt)
	respondJSON(w, http.StatusOK, map[string]any{"lease": current})
}

func (a *app) listNoTapRooms(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), noTapRoomSelect)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取无网卡房间")
		return
	}
	defer rows.Close()
	rooms := make([]room, 0, 6)
	for rows.Next() {
		var item room
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Region, &item.SubnetCIDR,
			&item.Capacity, &item.Status, &item.ConnectionMode, &item.Members); err != nil {
			respondError(w, http.StatusInternalServerError, "无法读取无网卡房间")
			return
		}
		rooms = append(rooms, item)
	}
	if err := rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取无网卡房间")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

func (a *app) getNoTapRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	var item room
	err := a.db.QueryRowContext(r.Context(), `
		SELECT r.id, r.code, r.name, r.region, r.subnet_cidr, r.capacity, r.status, r.connection_mode,
			COUNT(l.id) AS members
		FROM no_tap_rooms r
		LEFT JOIN no_tap_room_leases l
			ON l.room_id = r.id AND l.released_at IS NULL AND l.credential_expires_at > UTC_TIMESTAMP()
		WHERE r.id = ? AND r.id BETWEEN 1 AND 6
		GROUP BY r.id`, roomID).Scan(
		&item.ID, &item.Code, &item.Name, &item.Region, &item.SubnetCIDR, &item.Capacity,
		&item.Status, &item.ConnectionMode, &item.Members)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "无网卡房间不存在")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取无网卡房间")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"room": item})
}

func (a *app) listNoTapRoomMembers(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	var joined bool
	err := a.db.QueryRowContext(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM no_tap_room_leases
			WHERE room_id = ? AND user_id = ? AND session_id = ?
				AND released_at IS NULL AND credential_expires_at > UTC_TIMESTAMP()
		)`, roomID, currentUserID(r), currentSessionID(r)).Scan(&joined)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取无网卡房间成员")
		return
	}
	if !joined {
		respondError(w, http.StatusForbidden, "请先进入无网卡房间")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
		SELECT p.id, p.username_snapshot, p.nickname_snapshot, l.virtual_ip,
			COALESCE(l.real_ip, ''), l.user_id = ?,
		CASE WHEN r.connection_mode = 'direct' THEN COALESCE(l.ice_local_description, '') ELSE '' END,
			CASE WHEN r.connection_mode <> 'direct' THEN 'disabled'
			     WHEN l.ice_local_description IS NULL OR l.ice_local_description = '' THEN 'waiting'
			     ELSE 'ready' END
		FROM no_tap_room_leases l
		INNER JOIN no_tap_rooms r ON r.id = l.room_id
		INNER JOIN platform_users p ON p.id = l.user_id
		WHERE l.room_id = ? AND l.released_at IS NULL
			AND l.credential_expires_at > UTC_TIMESTAMP() AND p.status = 'active'
		ORDER BY l.user_id = ? DESC, p.nickname_snapshot, p.username_snapshot`,
		currentUserID(r), roomID, currentUserID(r))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取无网卡房间成员")
		return
	}
	defer rows.Close()
	members := make([]roomMember, 0)
	for rows.Next() {
		var member roomMember
		var virtualIP, realIP sql.NullString
		if err := rows.Scan(&member.UserID, &member.Username, &member.Nickname, &virtualIP, &realIP, &member.IsSelf, &member.IceDescription, &member.IceState); err != nil {
			respondError(w, http.StatusInternalServerError, "无法读取无网卡房间成员")
			return
		}
		member.VirtualIP = nullableString(virtualIP)
		member.RealIP = nullableString(realIP)
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取无网卡房间成员")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (a *app) joinNoTapRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID, sessionID := currentUserID(r), currentSessionID(r)
	requestIP := clientIP(r)
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法进入无网卡房间")
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
	var code, subnet, ipStart, ipEnd, status, connectionMode string
	var capacity int
	err = tx.QueryRowContext(r.Context(), `
		SELECT code, subnet_cidr, ip_start, ip_end, status, capacity, connection_mode
		FROM no_tap_rooms WHERE id = ? AND id BETWEEN 1 AND 6 FOR UPDATE`, roomID).Scan(&code, &subnet, &ipStart, &ipEnd, &status, &capacity, &connectionMode)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "无网卡房间不存在")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法进入无网卡房间")
		return
	}
	if status != "open" {
		respondError(w, http.StatusConflict, "无网卡房间暂不可进入")
		return
	}
	if connectionMode != "tap" && (a.config.noTapRelayHost == "" || a.config.noTapRelayPort == 0 || a.config.noTapRelayToken == "") {
		respondError(w, http.StatusServiceUnavailable, "无网卡中继尚未配置")
		return
	}
	var existingIP, existingUsername string
	err = tx.QueryRowContext(r.Context(), `
		SELECT virtual_ip, relay_username FROM no_tap_room_leases
		WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL
		ORDER BY id DESC LIMIT 1 FOR UPDATE`, roomID, userID, sessionID).Scan(&existingIP, &existingUsername)
	expiresAt := time.Now().UTC().Add(leaseTTL)
	if err == nil {
		if _, err := tx.ExecContext(r.Context(), `
			UPDATE no_tap_room_leases
			SET credential_expires_at = ?, real_ip = ?, state = 'connected', ice_local_description = NULL, ice_updated_at = NULL
			WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL`,
			expiresAt, requestIP, roomID, userID, sessionID); err != nil {
			respondError(w, http.StatusInternalServerError, "无法刷新无网卡连接凭据")
			return
		}
		if err := tx.Commit(); err != nil {
			respondError(w, http.StatusInternalServerError, "无法进入无网卡房间")
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"lease": a.noTapLeasePayload(roomID, code, subnet, existingIP, existingUsername, connectionMode, expiresAt)})
		return
	}
	if err != sql.ErrNoRows {
		respondError(w, http.StatusInternalServerError, "无法进入无网卡房间")
		return
	}
	var members int
	if err := tx.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM no_tap_room_leases
		WHERE room_id = ? AND released_at IS NULL AND credential_expires_at > UTC_TIMESTAMP()`, roomID).Scan(&members); err != nil {
		respondError(w, http.StatusInternalServerError, "无法进入无网卡房间")
		return
	}
	if members >= capacity {
		respondError(w, http.StatusConflict, "无网卡房间已满")
		return
	}
	assignedIP, err := nextFreeNoTapIP(r.Context(), tx, roomID, ipStart, ipEnd)
	if err != nil {
		respondError(w, http.StatusConflict, "无网卡房间逻辑地址已耗尽")
		return
	}
	username := fmt.Sprintf("notap-room-%d-user-%d-%d", roomID, userID, time.Now().Unix())
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO no_tap_room_leases
			(room_id, user_id, session_id, virtual_ip, relay_username, real_ip, credential_expires_at, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'connected')`,
		roomID, userID, sessionID, assignedIP, username, requestIP, expiresAt); err != nil {
		respondError(w, http.StatusInternalServerError, "无法分配无网卡逻辑地址")
		return
	}
	if err := tx.Commit(); err != nil {
		respondError(w, http.StatusInternalServerError, "无法进入无网卡房间")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"lease": a.noTapLeasePayload(roomID, code, subnet, assignedIP, username, connectionMode, expiresAt)})
}

func (a *app) heartbeatNoTapRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID, sessionID := currentUserID(r), currentSessionID(r)
	if current, err := a.isSessionCurrent(r.Context(), userID, sessionID); err != nil {
		respondError(w, http.StatusServiceUnavailable, "暂时无法验证登录状态")
		return
	} else if !current {
		respondErrorCode(w, http.StatusUnauthorized, "SESSION_REPLACED", "账号已在其他设备登录")
		return
	}
	expiresAt := time.Now().UTC().Add(leaseTTL)
	result, err := a.db.ExecContext(r.Context(), `
		UPDATE no_tap_room_leases
		SET credential_expires_at = ?, real_ip = ?
		WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL
			AND credential_expires_at > UTC_TIMESTAMP()`, expiresAt, clientIP(r), roomID, userID, sessionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法续期无网卡连接")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		respondError(w, http.StatusConflict, "无网卡连接已结束，请重新进入")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"expires_at": expiresAt})
}

func (a *app) leaveNoTapRoom(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok {
		return
	}
	userID, sessionID := currentUserID(r), currentSessionID(r)
	if current, err := a.isSessionCurrent(r.Context(), userID, sessionID); err != nil {
		respondError(w, http.StatusServiceUnavailable, "暂时无法验证登录状态")
		return
	} else if !current {
		respondErrorCode(w, http.StatusUnauthorized, "SESSION_REPLACED", "账号已在其他设备登录")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `
		DELETE FROM no_tap_room_leases
		WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL`, roomID, userID, sessionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法退出无网卡房间")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		respondError(w, http.StatusNotFound, "你不在该无网卡房间内")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type noTapICERequest struct {
	LocalDescription string `json:"local_description"`
}

const noTapPeerProbeTTL = 60 * time.Second
const wireGuardPeerTTL = 45 * time.Second
const noTapICEDescriptionMaxLength = 16384

type noTapPeerProbeRequest struct {
	TargetUserID     int64  `json:"target_user_id"`
	Purpose          string `json:"purpose"`
	SessionKey       string `json:"session_key"`
	LocalDescription string `json:"local_description"`
}

type noTapPeerProbeAnswerRequest struct {
	LocalDescription string `json:"local_description"`
}

func validNoTapICEDescription(value string) bool {
	length := len(value)
	return length >= 16 && length <= noTapICEDescriptionMaxLength && strings.Contains(value, "a=candidate:")
}

func normalizeNoTapProbePurpose(value string) string {
	if value == "game" {
		return "game"
	}
	return "ping"
}

func noTapProbeIDFromRequest(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "probeID")), 10, 64)
	if err != nil || id < 1 {
		respondError(w, http.StatusBadRequest, "直连探测编号无效")
		return 0, false
	}
	return id, true
}

func (a *app) requireNoTapRoomMember(w http.ResponseWriter, r *http.Request, roomID int64) bool {
	var joined bool
	err := a.db.QueryRowContext(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM no_tap_room_leases
			WHERE room_id = ? AND user_id = ? AND session_id = ?
				AND released_at IS NULL AND credential_expires_at > UTC_TIMESTAMP()
		)`, roomID, currentUserID(r), currentSessionID(r)).Scan(&joined)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法验证无网卡房间成员")
		return false
	}
	if !joined {
		respondError(w, http.StatusForbidden, "请先进入无网卡房间")
		return false
	}
	return true
}

func (a *app) requireNoTapDirectRoom(w http.ResponseWriter, r *http.Request, roomID int64) bool {
	var mode string
	if err := a.db.QueryRowContext(r.Context(), `SELECT connection_mode FROM no_tap_rooms WHERE id = ?`, roomID).Scan(&mode); err != nil {
		respondError(w, http.StatusInternalServerError, "无法确认房间连接模式")
		return false
	}
	if mode != "direct" {
		respondError(w, http.StatusConflict, "该房间仅使用云中继")
		return false
	}
	return true
}

func (a *app) requireWireGuardRoom(w http.ResponseWriter, r *http.Request, roomID int64) bool {
	var mode string
	if err := a.db.QueryRowContext(r.Context(), `SELECT connection_mode FROM no_tap_rooms WHERE id = ?`, roomID).Scan(&mode); err != nil {
		respondError(w, http.StatusInternalServerError, "无法确认网卡房间模式")
		return false
	}
	if mode != "wireguard" {
		respondError(w, http.StatusConflict, "该房间不使用网卡模式")
		return false
	}
	return true
}

func validWireGuardPublicKey(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) == 44 && strings.HasSuffix(value, "=")
}

func validWireGuardEndpointHost(value string) bool {
	host := strings.TrimSpace(value)
	if len(host) == 0 || len(host) > 255 || strings.ContainsAny(host, " \t\r\n/\\") {
		return false
	}
	return true
}

type wireGuardControllerPeer struct {
	EndpointHost string `json:"endpoint_host"`
	EndpointPort int    `json:"endpoint_port"`
}

func (a *app) wireGuardControllerRequest(ctx context.Context, method, path string, body any, result any) error {
	if a.config.wireGuardControllerURL == "" || a.config.wireGuardControllerSecret == "" {
		return fmt.Errorf("wireguard controller is not configured")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.config.wireGuardControllerURL, "/")+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+a.config.wireGuardControllerSecret)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("wireguard controller status %d", response.StatusCode)
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(result)
}

func (a *app) registerWireGuardClient(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapRoomMember(w, r, roomID) || !a.requireWireGuardRoom(w, r, roomID) {
		return
	}
	var request wireGuardClientRequest
	if !decodeJSON(w, r, &request) || !validWireGuardPublicKey(request.PublicKey) {
		respondError(w, http.StatusBadRequest, "网卡公钥无效")
		return
	}
	var virtualIP, previousPublicKey, previousVirtualIP string
	err := a.db.QueryRowContext(r.Context(), `
		SELECT virtual_ip FROM no_tap_room_leases
		WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL
			AND credential_expires_at > UTC_TIMESTAMP()`, roomID, currentUserID(r), currentSessionID(r)).Scan(&virtualIP)
	if err != nil {
		respondError(w, http.StatusConflict, "网卡房间连接已结束，请重新进入")
		return
	}
	// Re-entering a WireGuard room creates a fresh temporary key. Capture the
	// previous key before replacing the database row so the controller can
	// remove its stale peer from the shared interface.
	_ = a.db.QueryRowContext(r.Context(), `
		SELECT public_key, virtual_ip FROM no_tap_wireguard_clients
		WHERE room_id = ? AND user_id = ? AND session_id = ?
		ORDER BY updated_at DESC LIMIT 1`, roomID, currentUserID(r), currentSessionID(r)).Scan(&previousPublicKey, &previousVirtualIP)
	expiresAt := time.Now().UTC().Add(leaseTTL)
	if err := a.wireGuardControllerRequest(r.Context(), http.MethodPost, "/clients", map[string]any{
		"room_id": roomID, "public_key": strings.TrimSpace(request.PublicKey),
		"previous_public_key": strings.TrimSpace(previousPublicKey), "previous_virtual_ip": strings.TrimSpace(previousVirtualIP), "virtual_ip": virtualIP,
	}, nil); err != nil {
		respondError(w, http.StatusServiceUnavailable, "网卡服务端暂不可用")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `
		INSERT INTO no_tap_wireguard_clients (room_id, user_id, session_id, public_key, virtual_ip, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE public_key=VALUES(public_key), virtual_ip=VALUES(virtual_ip), expires_at=VALUES(expires_at)`,
		roomID, currentUserID(r), currentSessionID(r), strings.TrimSpace(request.PublicKey), virtualIP, expiresAt); err != nil {
		// The controller was updated before the database row. Roll the new
		// peer back if persistence fails so a later client cannot inherit an
		// untracked virtual address.
		_ = a.wireGuardControllerRequest(r.Context(), http.MethodDelete, "/clients/"+url.PathEscape(strings.TrimSpace(request.PublicKey)), nil, nil)
		respondError(w, http.StatusInternalServerError, "无法登记网卡客户端")
		return
	}
	// A new client registration starts a new match lifecycle. Remove stale
	// pair records in either direction before the next GAME_PEER event.
	_, _ = a.db.ExecContext(r.Context(), `
		DELETE FROM no_tap_wireguard_peers
		WHERE room_id = ? AND ((user_id = ? AND session_id = ?) OR target_user_id = ?)`,
		roomID, currentUserID(r), currentSessionID(r), currentUserID(r))
	respondJSON(w, http.StatusOK, map[string]any{
		"state": "ready", "virtual_ip": virtualIP, "listen_port": a.config.wireGuardListenPort, "expires_at": expiresAt,
	})
}

func (a *app) publishWireGuardPeer(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapRoomMember(w, r, roomID) || !a.requireWireGuardRoom(w, r, roomID) {
		return
	}
	var request wireGuardPeerRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.MatchKey = strings.TrimSpace(request.MatchKey)
	request.PublicKey = strings.TrimSpace(request.PublicKey)
	if request.TargetUserID < 1 || request.TargetUserID == currentUserID(r) || len(request.MatchKey) < 3 || len(request.MatchKey) > 128 || !validWireGuardPublicKey(request.PublicKey) {
		respondError(w, http.StatusBadRequest, "网卡对手参数无效")
		return
	}
	var virtualIP, registeredPublicKey string
	err := a.db.QueryRowContext(r.Context(), `
		SELECT l.virtual_ip, c.public_key
		FROM no_tap_room_leases l
		JOIN no_tap_wireguard_clients c ON c.room_id = l.room_id AND c.user_id = l.user_id AND c.session_id = l.session_id
		WHERE l.room_id = ? AND l.user_id = ? AND l.session_id = ? AND l.released_at IS NULL
			AND l.credential_expires_at > UTC_TIMESTAMP() AND c.expires_at > UTC_TIMESTAMP()`,
		roomID, currentUserID(r), currentSessionID(r)).Scan(&virtualIP, &registeredPublicKey)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusConflict, "网卡房间连接已结束，请重新进入")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取网卡房间连接")
		return
	}
	if registeredPublicKey != request.PublicKey {
		respondError(w, http.StatusForbidden, "网卡公钥与当前房间连接不匹配")
		return
	}
	var observed wireGuardControllerPeer
	if err := a.wireGuardControllerRequest(r.Context(), http.MethodGet, "/clients/"+url.PathEscape(request.PublicKey), nil, &observed); err != nil ||
		!validWireGuardEndpointHost(observed.EndpointHost) || observed.EndpointPort < 1 || observed.EndpointPort > 65535 {
		respondError(w, http.StatusConflict, "网卡尚未取得公网端点，请稍后重试")
		return
	}
	var targetPresent bool
	if err := a.db.QueryRowContext(r.Context(), `
		SELECT EXISTS(SELECT 1 FROM no_tap_room_leases WHERE room_id = ? AND user_id = ?
			AND released_at IS NULL AND credential_expires_at > UTC_TIMESTAMP())`, roomID, request.TargetUserID).Scan(&targetPresent); err != nil || !targetPresent {
		respondError(w, http.StatusConflict, "网卡对手已不在房间")
		return
	}
	expiresAt := time.Now().UTC().Add(wireGuardPeerTTL)
	if _, err := a.db.ExecContext(r.Context(), `
		INSERT INTO no_tap_wireguard_peers
			(room_id, user_id, target_user_id, session_id, match_key, public_key, endpoint_host, endpoint_port, virtual_ip, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE public_key=VALUES(public_key), endpoint_host=VALUES(endpoint_host),
			endpoint_port=VALUES(endpoint_port), virtual_ip=VALUES(virtual_ip), expires_at=VALUES(expires_at)`,
		roomID, currentUserID(r), request.TargetUserID, currentSessionID(r), request.MatchKey, request.PublicKey,
		observed.EndpointHost, observed.EndpointPort, virtualIP, expiresAt); err != nil {
		respondError(w, http.StatusInternalServerError, "无法保存网卡对手信息")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"state": "ready", "expires_at": expiresAt})
}

func (a *app) getWireGuardPeer(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapRoomMember(w, r, roomID) || !a.requireWireGuardRoom(w, r, roomID) {
		return
	}
	matchKey := strings.TrimSpace(chi.URLParam(r, "matchKey"))
	if len(matchKey) < 3 || len(matchKey) > 128 {
		respondError(w, http.StatusBadRequest, "网卡比赛事务键无效")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
		SELECT user_id, match_key, public_key, endpoint_host, endpoint_port, virtual_ip, expires_at
		FROM no_tap_wireguard_peers
		WHERE room_id = ? AND match_key = ? AND target_user_id = ? AND user_id <> ? AND expires_at > UTC_TIMESTAMP()
		ORDER BY updated_at DESC LIMIT 1`, roomID, matchKey, currentUserID(r), currentUserID(r))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取网卡对手信息")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		respondJSON(w, http.StatusOK, map[string]any{"peer": nil})
		return
	}
	var peer wireGuardPeer
	if err := rows.Scan(&peer.UserID, &peer.MatchKey, &peer.PublicKey, &peer.EndpointHost, &peer.EndpointPort, &peer.VirtualIP, &peer.ExpiresAt); err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取网卡对手信息")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"peer": peer})
}

func (a *app) createNoTapPeerProbe(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapRoomMember(w, r, roomID) || !a.requireNoTapDirectRoom(w, r, roomID) {
		return
	}
	var request noTapPeerProbeRequest
	if !decodeJSON(w, r, &request) || request.TargetUserID < 1 || request.TargetUserID == currentUserID(r) || !validNoTapICEDescription(request.LocalDescription) {
		respondError(w, http.StatusBadRequest, "直连探测参数无效")
		return
	}
	request.Purpose = normalizeNoTapProbePurpose(request.Purpose)
	if request.Purpose == "game" && (len(request.SessionKey) < 3 || len(request.SessionKey) > 128) {
		respondError(w, http.StatusBadRequest, "比赛直连事务键无效")
		return
	}

	var targetPresent bool
	err := a.db.QueryRowContext(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM no_tap_room_leases
			WHERE room_id = ? AND user_id = ? AND released_at IS NULL
				AND credential_expires_at > UTC_TIMESTAMP()
		)`, roomID, request.TargetUserID).Scan(&targetPresent)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法创建直连探测")
		return
	}
	if !targetPresent {
		respondError(w, http.StatusConflict, "目标玩家已不在房间")
		return
	}

	// A second click for the same target supersedes an unfinished probe. Keeping
	// stale rows here can fill the target's polling window in a busy room.
	if _, err := a.db.ExecContext(r.Context(), `
		DELETE FROM no_tap_peer_probes
		WHERE room_id = ? AND requester_user_id = ? AND target_user_id = ? AND purpose = ?
			AND target_description IS NULL`,
		roomID, currentUserID(r), request.TargetUserID, request.Purpose); err != nil {
		respondError(w, http.StatusInternalServerError, "无法清理旧的直连探测")
		return
	}

	expiresAt := time.Now().UTC().Add(noTapPeerProbeTTL)
	result, err := a.db.ExecContext(r.Context(), `
		INSERT INTO no_tap_peer_probes
			(room_id, requester_user_id, target_user_id, purpose, session_key, requester_description, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, roomID, currentUserID(r), request.TargetUserID, request.Purpose, request.SessionKey, request.LocalDescription, expiresAt)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法创建直连探测")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取直连探测")
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"probe": noTapPeerProbe{
		ID: id, RequesterUserID: currentUserID(r), TargetUserID: request.TargetUserID, Purpose: request.Purpose, SessionKey: request.SessionKey, ExpiresAt: expiresAt,
	}})
}

func (a *app) listIncomingNoTapPeerProbes(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapRoomMember(w, r, roomID) || !a.requireNoTapDirectRoom(w, r, roomID) {
		return
	}
	purpose := normalizeNoTapProbePurpose(r.URL.Query().Get("purpose"))
	rows, err := a.db.QueryContext(r.Context(), `
		SELECT id, requester_user_id, target_user_id, purpose, COALESCE(session_key, ''), requester_description, expires_at
		FROM no_tap_peer_probes
		WHERE room_id = ? AND target_user_id = ? AND purpose = ? AND target_description IS NULL
			AND expires_at > UTC_TIMESTAMP()
		ORDER BY id DESC LIMIT 32`, roomID, currentUserID(r), purpose)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取直连探测")
		return
	}
	defer rows.Close()
	probes := make([]noTapPeerProbe, 0)
	for rows.Next() {
		var probe noTapPeerProbe
		if err := rows.Scan(&probe.ID, &probe.RequesterUserID, &probe.TargetUserID, &probe.Purpose, &probe.SessionKey, &probe.RequesterDescription, &probe.ExpiresAt); err != nil {
			respondError(w, http.StatusInternalServerError, "无法读取直连探测")
			return
		}
		probes = append(probes, probe)
	}
	if err := rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取直连探测")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"probes": probes})
}

func (a *app) getNoTapPeerProbe(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapRoomMember(w, r, roomID) || !a.requireNoTapDirectRoom(w, r, roomID) {
		return
	}
	probeID, ok := noTapProbeIDFromRequest(w, r)
	if !ok {
		return
	}
	var probe noTapPeerProbe
	err := a.db.QueryRowContext(r.Context(), `
		SELECT id, requester_user_id, target_user_id, purpose, COALESCE(session_key, ''), requester_description,
			COALESCE(target_description, ''), expires_at
		FROM no_tap_peer_probes
		WHERE id = ? AND room_id = ? AND (requester_user_id = ? OR target_user_id = ?)
			AND expires_at > UTC_TIMESTAMP()`, probeID, roomID, currentUserID(r), currentUserID(r)).Scan(
		&probe.ID, &probe.RequesterUserID, &probe.TargetUserID, &probe.Purpose, &probe.SessionKey, &probe.RequesterDescription, &probe.TargetDescription, &probe.ExpiresAt)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "直连探测已结束")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法读取直连探测")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"probe": probe})
}

func (a *app) answerNoTapPeerProbe(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapRoomMember(w, r, roomID) || !a.requireNoTapDirectRoom(w, r, roomID) {
		return
	}
	probeID, ok := noTapProbeIDFromRequest(w, r)
	if !ok {
		return
	}
	var request noTapPeerProbeAnswerRequest
	if !decodeJSON(w, r, &request) || !validNoTapICEDescription(request.LocalDescription) {
		respondError(w, http.StatusBadRequest, "直连探测 candidate 无效")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `
		UPDATE no_tap_peer_probes
		SET target_description = ?
		WHERE id = ? AND room_id = ? AND target_user_id = ? AND target_description IS NULL
			AND expires_at > UTC_TIMESTAMP()`, request.LocalDescription, probeID, roomID, currentUserID(r))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法响应直连探测")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		respondError(w, http.StatusConflict, "直连探测已结束或已响应")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"state": "answered"})
}

func (a *app) publishNoTapICE(w http.ResponseWriter, r *http.Request) {
	roomID, ok := roomIDFromRequest(w, r)
	if !ok || !a.requireNoTapDirectRoom(w, r, roomID) {
		return
	}
	var request noTapICERequest
	if !decodeJSON(w, r, &request) || !validNoTapICEDescription(request.LocalDescription) {
		respondError(w, http.StatusBadRequest, "ICE candidate 数据无效")
		return
	}
	if current, err := a.isSessionCurrent(r.Context(), currentUserID(r), currentSessionID(r)); err != nil {
		respondError(w, http.StatusServiceUnavailable, "暂时无法验证登录状态")
		return
	} else if !current {
		respondErrorCode(w, http.StatusUnauthorized, "SESSION_REPLACED", "账号已在其他设备登录")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `
		UPDATE no_tap_room_leases
		SET ice_local_description = ?, ice_updated_at = UTC_TIMESTAMP()
		WHERE room_id = ? AND user_id = ? AND session_id = ? AND released_at IS NULL
			AND credential_expires_at > UTC_TIMESTAMP()`, request.LocalDescription, roomID, currentUserID(r), currentSessionID(r))
	if err != nil {
		respondError(w, http.StatusInternalServerError, "无法保存 ICE candidate")
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		respondError(w, http.StatusConflict, "无网卡房间连接已结束，请重新进入")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"state": "ready"})
}

func nextFreeNoTapIP(ctx context.Context, tx *sql.Tx, roomID int64, start, end string) (string, error) {
	from, to := parseIPv4(start), parseIPv4(end)
	if from == nil || to == nil {
		return "", fmt.Errorf("invalid no-TAP room ip range")
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT virtual_ip FROM no_tap_room_leases
		WHERE room_id = ? AND released_at IS NULL FOR UPDATE`, roomID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	used := map[string]bool{}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return "", err
		}
		used[ip] = true
	}
	for current := append([]byte(nil), from...); ; incrementIPv4Bytes(current) {
		candidate := fmt.Sprintf("%d.%d.%d.%d", current[0], current[1], current[2], current[3])
		if !used[candidate] {
			return candidate, nil
		}
		if string(current) == string(to) {
			break
		}
	}
	return "", fmt.Errorf("no free no-TAP room ip")
}

func parseIPv4(value string) []byte {
	var a, b, c, d int
	if _, err := fmt.Sscanf(value, "%d.%d.%d.%d", &a, &b, &c, &d); err != nil || a < 0 || a > 255 || b < 0 || b > 255 || c < 0 || c > 255 || d < 0 || d > 255 {
		return nil
	}
	return []byte{byte(a), byte(b), byte(c), byte(d)}
}

func incrementIPv4Bytes(ip []byte) {
	for index := len(ip) - 1; index >= 0; index-- {
		ip[index]++
		if ip[index] != 0 {
			return
		}
	}
}
