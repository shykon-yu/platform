package platformdb

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

const (
	soccerIdentityMigration = "20260803_soccer_identity"
	sixRoomsMigration       = "20260803_limit_rooms_to_six"
	singleSessionMigration  = "20260805_single_active_session"
	vpnUsernameMigration    = "20260806_rename_legacy_vpn_username"
	roomRealIPMigration     = "20260806_add_room_real_ip"
	roomSubnet222Migration  = "20260809_move_rooms_to_10_222"
	dynamicOpenVPNMigration = "20260809_dynamic_openvpn_ip"
	n2nStaticIPMigration    = "20260810_n2n_static_room_ip"
	noTapRoomsMigration     = "20260814_create_no_tap_rooms"
	noTapICEMigration       = "20260815_add_no_tap_ice_description"
	noTapRoomNamesMigration = "20260816_rename_no_tap_room_labels"
	noTapPeerProbeMigration = "20260816_add_no_tap_peer_probes"
	noTapGameProbeMigration = "20260817_add_no_tap_game_probe_fields"
	noTapRoomModesMigration = "20260818_add_no_tap_room_modes"
	noTapRoomFourMigration  = "20260818_add_no_tap_room_04"
	noTapTapRoomsMigration  = "20260908_add_no_tap_tap_rooms"
	noTapRoomLayoutMigration = "20260909_set_no_tap_room_layout"
	noTapWireGuardRoomsMigration = "20260910_add_no_tap_wireguard_rooms"
	noTapWireGuardPeersMigration = "20260910_add_no_tap_wireguard_peers"
	noTapWireGuardClientsMigration = "20260910_add_no_tap_wireguard_clients"
)

var safeIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS platform_schema_migrations (
			version VARCHAR(64) NOT NULL PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create platform migrations table: %w", err)
	}

	if err := runMigration(ctx, db, soccerIdentityMigration, migrateSoccerIdentity); err != nil {
		return err
	}
	if err := runMigration(ctx, db, sixRoomsMigration, migrateLimitRoomsToSix); err != nil {
		return err
	}
	if err := runMigration(ctx, db, singleSessionMigration, migrateSingleActiveSession); err != nil {
		return err
	}
	if err := runMigration(ctx, db, vpnUsernameMigration, migrateVpnUsername); err != nil {
		return err
	}
	if err := runMigration(ctx, db, roomRealIPMigration, migrateRoomRealIP); err != nil {
		return err
	}
	if err := runMigration(ctx, db, roomSubnet222Migration, migrateRoomSubnet222); err != nil {
		return err
	}
	if err := runMigration(ctx, db, dynamicOpenVPNMigration, migrateDynamicOpenVPNIP); err != nil {
		return err
	}
	if err := runMigration(ctx, db, n2nStaticIPMigration, migrateN2NStaticRoomIP); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapRoomsMigration, migrateNoTapRooms); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapICEMigration, migrateNoTapICE); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapRoomNamesMigration, migrateNoTapRoomNames); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapPeerProbeMigration, migrateNoTapPeerProbes); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapGameProbeMigration, migrateNoTapGameProbeFields); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapRoomModesMigration, migrateNoTapRoomModes); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapRoomFourMigration, migrateNoTapRoomFour); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapTapRoomsMigration, migrateNoTapTapRooms); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapRoomLayoutMigration, migrateNoTapRoomLayout); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapWireGuardRoomsMigration, migrateNoTapWireGuardRooms); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapWireGuardPeersMigration, migrateNoTapWireGuardPeers); err != nil {
		return err
	}
	if err := runMigration(ctx, db, noTapWireGuardClientsMigration, migrateNoTapWireGuardClients); err != nil {
		return err
	}
	return nil
}

// migrateNoTapWireGuardRooms adds the experimental layer-3 room pair. The
// rooms keep the existing No-TAP relay credentials so clients without a
// WireGuard runtime can still use the tested Hook/relay fallback path.
func migrateNoTapWireGuardRooms(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE no_tap_rooms MODIFY connection_mode ENUM('tap', 'direct', 'relay', 'wireguard') NOT NULL DEFAULT 'direct'`); err != nil {
		return fmt.Errorf("expand no-TAP room modes for WireGuard: %w", err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO no_tap_rooms
			(id, code, name, region, connection_mode, subnet_cidr, ip_start, ip_end, capacity, status, sort_order)
		VALUES
			(7, 'notap-07', '网卡07', '网卡', 'wireguard', '10.222.7.0/24', '10.222.7.10', '10.222.7.109', 100, 'open', 7),
			(8, 'notap-08', '网卡08', '网卡', 'wireguard', '10.222.8.0/24', '10.222.8.10', '10.222.8.109', 100, 'open', 8)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name), region = VALUES(region), connection_mode = VALUES(connection_mode),
			subnet_cidr = VALUES(subnet_cidr), ip_start = VALUES(ip_start), ip_end = VALUES(ip_end),
			status = VALUES(status), sort_order = VALUES(sort_order)`)
	if err != nil {
		return fmt.Errorf("seed no-TAP WireGuard rooms: %w", err)
	}
	return nil
}

func migrateNoTapWireGuardPeers(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS no_tap_wireguard_peers (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			room_id BIGINT UNSIGNED NOT NULL,
			user_id BIGINT UNSIGNED NOT NULL,
			target_user_id BIGINT UNSIGNED NOT NULL,
			session_id VARCHAR(43) NOT NULL,
			match_key VARCHAR(128) NOT NULL,
			public_key VARCHAR(64) NOT NULL,
			endpoint_host VARCHAR(255) NOT NULL,
			endpoint_port SMALLINT UNSIGNED NOT NULL,
			virtual_ip VARCHAR(15) NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY no_tap_wg_peer_session (room_id, user_id, target_user_id, session_id, match_key),
			KEY no_tap_wg_peer_match (room_id, target_user_id, match_key, expires_at),
			CONSTRAINT no_tap_wg_peer_room_foreign FOREIGN KEY (room_id) REFERENCES no_tap_rooms (id),
			CONSTRAINT no_tap_wg_peer_user_foreign FOREIGN KEY (user_id) REFERENCES platform_users (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create no-TAP WireGuard peers: %w", err)
	}
	for _, column := range []struct{ name, sql string }{
		{name: "target_user_id", sql: `ALTER TABLE no_tap_wireguard_peers ADD COLUMN target_user_id BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER user_id`},
		{name: "endpoint_host", sql: `ALTER TABLE no_tap_wireguard_peers ADD COLUMN endpoint_host VARCHAR(255) NOT NULL DEFAULT '' AFTER public_key`},
		{name: "endpoint_port", sql: `ALTER TABLE no_tap_wireguard_peers ADD COLUMN endpoint_port SMALLINT UNSIGNED NOT NULL DEFAULT 0 AFTER endpoint_host`},
	} {
		exists, err := columnExists(ctx, db, "no_tap_wireguard_peers", column.name)
		if err != nil { return err }
		if !exists {
			if _, err := db.ExecContext(ctx, column.sql); err != nil { return fmt.Errorf("add no-TAP WireGuard peer %s: %w", column.name, err) }
		}
	}
	return nil
}

func migrateNoTapWireGuardClients(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS no_tap_wireguard_clients (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			room_id BIGINT UNSIGNED NOT NULL,
			user_id BIGINT UNSIGNED NOT NULL,
			session_id VARCHAR(43) NOT NULL,
			public_key VARCHAR(64) NOT NULL,
			virtual_ip VARCHAR(15) NOT NULL,
			endpoint_host VARCHAR(255) NULL,
			endpoint_port SMALLINT UNSIGNED NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY no_tap_wg_client_session (room_id, user_id, session_id),
			KEY no_tap_wg_client_key (public_key, expires_at),
			CONSTRAINT no_tap_wg_client_room_foreign FOREIGN KEY (room_id) REFERENCES no_tap_rooms (id),
			CONSTRAINT no_tap_wg_client_user_foreign FOREIGN KEY (user_id) REFERENCES platform_users (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create no-TAP WireGuard clients: %w", err)
	}
	for _, column := range []struct{ name, sql string }{
		{name: "endpoint_host", sql: `ALTER TABLE no_tap_wireguard_clients ADD COLUMN endpoint_host VARCHAR(255) NULL AFTER virtual_ip`},
		{name: "endpoint_port", sql: `ALTER TABLE no_tap_wireguard_clients ADD COLUMN endpoint_port SMALLINT UNSIGNED NULL AFTER endpoint_host`},
	} {
		exists, err := columnExists(ctx, db, "no_tap_wireguard_clients", column.name)
		if err != nil { return err }
		if !exists {
			if _, err := db.ExecContext(ctx, column.sql); err != nil { return fmt.Errorf("add no-TAP WireGuard client %s: %w", column.name, err) }
		}
	}
	return nil
}

func migrateNoTapRooms(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS no_tap_rooms (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			code VARCHAR(32) NOT NULL,
			name VARCHAR(64) NOT NULL,
			region VARCHAR(32) NOT NULL,
			connection_mode ENUM('tap', 'direct', 'relay', 'wireguard') NOT NULL DEFAULT 'direct',
			subnet_cidr VARCHAR(32) NOT NULL,
			ip_start VARCHAR(15) NOT NULL,
			ip_end VARCHAR(15) NOT NULL,
			capacity SMALLINT UNSIGNED NOT NULL DEFAULT 100,
			status ENUM('open', 'maintenance', 'closed') NOT NULL DEFAULT 'open',
			sort_order INT NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY no_tap_rooms_code_unique (code)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create no-TAP rooms: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS no_tap_room_leases (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			room_id BIGINT UNSIGNED NOT NULL,
			user_id BIGINT UNSIGNED NOT NULL,
			session_id VARCHAR(43) NOT NULL,
			virtual_ip VARCHAR(15) NOT NULL,
			state ENUM('allocated', 'connected', 'released') NOT NULL DEFAULT 'connected',
			relay_username VARCHAR(96) NOT NULL,
			real_ip VARCHAR(45) NULL,
			ice_local_description TEXT NULL,
			ice_updated_at DATETIME NULL,
			credential_expires_at DATETIME NOT NULL,
			released_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY no_tap_leases_room_ip_unique (room_id, virtual_ip),
			UNIQUE KEY no_tap_leases_active_user (room_id, user_id),
			CONSTRAINT no_tap_leases_room_foreign FOREIGN KEY (room_id) REFERENCES no_tap_rooms (id),
			CONSTRAINT no_tap_leases_user_foreign FOREIGN KEY (user_id) REFERENCES platform_users (id),
			KEY no_tap_leases_user_index (user_id),
			KEY no_tap_leases_state_index (state)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create no-TAP leases: %w", err)
	}
	for index := 1; index <= 6; index++ {
		code := fmt.Sprintf("notap-%02d", index)
		name := fmt.Sprintf("房间 %02d", index)
		region, mode, subnetPrefix := "中继", "relay", "10.122"
		// Keep this first seed compatible with databases whose enum predates TAP;
		// the follow-up migration expands the enum and applies the final modes.
		if index <= 4 { region, mode = "直连", "direct" }
		subnet := fmt.Sprintf("%s.%d.0/24", subnetPrefix, index)
		start := fmt.Sprintf("%s.%d.10", subnetPrefix, index)
		end := fmt.Sprintf("%s.%d.109", subnetPrefix, index)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO no_tap_rooms (id, code, name, region, connection_mode, subnet_cidr, ip_start, ip_end, capacity, sort_order)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 100, ?)
			ON DUPLICATE KEY UPDATE name = VALUES(name), region = VALUES(region), connection_mode = VALUES(connection_mode), subnet_cidr = VALUES(subnet_cidr), ip_start = VALUES(ip_start), ip_end = VALUES(ip_end), sort_order = VALUES(sort_order)`, index, code, name, region, mode, subnet, start, end, index); err != nil {
			return fmt.Errorf("seed no-TAP room %d: %w", index, err)
		}
	}
	return nil
}

func migrateNoTapRoomModes(ctx context.Context, db *sql.DB) error {
	column, err := columnExists(ctx, db, "no_tap_rooms", "connection_mode")
	if err != nil {
		return err
	}
	if !column {
		if _, err := db.ExecContext(ctx, `ALTER TABLE no_tap_rooms ADD COLUMN connection_mode ENUM('tap', 'direct', 'relay', 'wireguard') NOT NULL DEFAULT 'direct' AFTER region`); err != nil {
			return fmt.Errorf("add no-TAP room mode: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE no_tap_rooms
		SET connection_mode = CASE WHEN id IN (1, 2) THEN 'direct' ELSE 'relay' END,
			region = CASE WHEN id IN (1, 2) THEN '直连' ELSE '中继' END
		WHERE id BETWEEN 1 AND 4`); err != nil {
		return fmt.Errorf("seed no-TAP room modes: %w", err)
	}
	return nil
}

func migrateNoTapTapRooms(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE no_tap_rooms MODIFY connection_mode ENUM('tap', 'direct', 'relay', 'wireguard') NOT NULL DEFAULT 'direct'`); err != nil {
		return fmt.Errorf("expand no-TAP room modes: %w", err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO no_tap_rooms (id, code, name, region, connection_mode, subnet_cidr, ip_start, ip_end, capacity, status, sort_order) VALUES
		(1, 'notap-01', '直连01', '直连', 'direct', '10.122.1.0/24', '10.122.1.10', '10.122.1.109', 100, 'open', 1),
		(2, 'notap-02', '直连02', '直连', 'direct', '10.122.2.0/24', '10.122.2.10', '10.122.2.109', 100, 'open', 2),
		(3, 'notap-03', '中继03', '中继', 'relay', '10.122.3.0/24', '10.122.3.10', '10.122.3.109', 100, 'open', 3),
		(4, 'notap-04', '中继04', '中继', 'relay', '10.122.4.0/24', '10.122.4.10', '10.122.4.109', 100, 'open', 4),
		(5, 'notap-05', '网卡05', '网卡', 'tap', '10.222.5.0/24', '10.222.5.10', '10.222.5.109', 100, 'open', 5),
		(6, 'notap-06', '网卡06', '网卡', 'tap', '10.222.6.0/24', '10.222.6.10', '10.222.6.109', 100, 'open', 6)
		ON DUPLICATE KEY UPDATE name=VALUES(name), region=VALUES(region), connection_mode=VALUES(connection_mode), subnet_cidr=VALUES(subnet_cidr), ip_start=VALUES(ip_start), ip_end=VALUES(ip_end), sort_order=VALUES(sort_order)`)
	if err != nil { return fmt.Errorf("seed no-TAP six transport rooms: %w", err) }
	return nil
}

// migrateNoTapRoomLayout applies the user-facing six-room layout to existing
// installations. The previous migration created TAP rooms 01/02 and direct
// rooms 03/04, while the client labels already exposed the new layout.
func migrateNoTapRoomLayout(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		UPDATE no_tap_rooms
		SET
			name = CASE id
				WHEN 1 THEN '直连01'
				WHEN 2 THEN '直连02'
				WHEN 3 THEN '中继03'
				WHEN 4 THEN '中继04'
				WHEN 5 THEN '网卡05'
				WHEN 6 THEN '网卡06'
			END,
			region = CASE
				WHEN id IN (1, 2) THEN '直连'
				WHEN id IN (3, 4) THEN '中继'
				ELSE '网卡'
			END,
			connection_mode = CASE
				WHEN id IN (1, 2) THEN 'direct'
				WHEN id IN (3, 4) THEN 'relay'
				ELSE 'tap'
			END,
			subnet_cidr = CASE
				WHEN id IN (1, 2, 3, 4) THEN CONCAT('10.122.', id, '.0/24')
				ELSE CONCAT('10.222.', id, '.0/24')
			END,
			ip_start = CASE
				WHEN id IN (1, 2, 3, 4) THEN CONCAT('10.122.', id, '.10')
				ELSE CONCAT('10.222.', id, '.10')
			END,
			ip_end = CASE
				WHEN id IN (1, 2, 3, 4) THEN CONCAT('10.122.', id, '.109')
				ELSE CONCAT('10.222.', id, '.109')
			END
		WHERE id BETWEEN 1 AND 6`)
	if err != nil {
		return fmt.Errorf("apply no-TAP room layout: %w", err)
	}
	return nil
}

func migrateNoTapRoomFour(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		INSERT INTO no_tap_rooms
			(id, code, name, region, connection_mode, subnet_cidr, ip_start, ip_end, capacity, status, sort_order)
		VALUES
			(4, 'notap-04', '房间 04', '中继', 'relay', '10.122.4.0/24', '10.122.4.10', '10.122.4.109', 100, 'open', 4)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name), region = VALUES(region), connection_mode = VALUES(connection_mode),
			subnet_cidr = VALUES(subnet_cidr), ip_start = VALUES(ip_start), ip_end = VALUES(ip_end),
			capacity = VALUES(capacity), sort_order = VALUES(sort_order)`); err != nil {
		return fmt.Errorf("seed no-TAP room 4: %w", err)
	}
	return nil
}

func migrateNoTapRoomNames(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		UPDATE no_tap_rooms
		SET name = CONCAT('房间 ', LPAD(id, 2, '0')), region = CASE WHEN id IN (1, 2) THEN '直连' ELSE '中继' END
		WHERE id BETWEEN 1 AND 4`); err != nil {
		return fmt.Errorf("rename no-TAP room labels: %w", err)
	}
	return nil
}

func migrateNoTapICE(ctx context.Context, db *sql.DB) error {
	column, err := columnExists(ctx, db, "no_tap_room_leases", "ice_local_description")
	if err != nil {
		return err
	}
	if !column {
		if _, err := db.ExecContext(ctx, `ALTER TABLE no_tap_room_leases ADD COLUMN ice_local_description TEXT NULL AFTER real_ip`); err != nil {
			return fmt.Errorf("add no-TAP ICE description: %w", err)
		}
	}
	column, err = columnExists(ctx, db, "no_tap_room_leases", "ice_updated_at")
	if err != nil {
		return err
	}
	if !column {
		if _, err := db.ExecContext(ctx, `ALTER TABLE no_tap_room_leases ADD COLUMN ice_updated_at DATETIME NULL AFTER ice_local_description`); err != nil {
			return fmt.Errorf("add no-TAP ICE timestamp: %w", err)
		}
	}
	return nil
}

// Peer probes exchange short-lived, pair-specific ICE descriptions. They are
// intentionally separate from the room-level game candidate so inspecting a
// member's latency can never replace the active game's remote ICE peer.
func migrateNoTapPeerProbes(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS no_tap_peer_probes (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			room_id BIGINT UNSIGNED NOT NULL,
			requester_user_id BIGINT UNSIGNED NOT NULL,
			target_user_id BIGINT UNSIGNED NOT NULL,
			requester_description TEXT NOT NULL,
			target_description TEXT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			KEY no_tap_peer_probes_target_index (room_id, target_user_id, expires_at),
			KEY no_tap_peer_probes_requester_index (room_id, requester_user_id, expires_at),
			CONSTRAINT no_tap_peer_probes_room_foreign FOREIGN KEY (room_id) REFERENCES no_tap_rooms (id),
			CONSTRAINT no_tap_peer_probes_requester_foreign FOREIGN KEY (requester_user_id) REFERENCES platform_users (id),
			CONSTRAINT no_tap_peer_probes_target_foreign FOREIGN KEY (target_user_id) REFERENCES platform_users (id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create no-TAP peer probes: %w", err)
	}
	return nil
}

func migrateNoTapGameProbeFields(ctx context.Context, db *sql.DB) error {
	for _, field := range []struct {
		name string
		sql  string
	}{
		{name: "purpose", sql: `ALTER TABLE no_tap_peer_probes ADD COLUMN purpose VARCHAR(16) NOT NULL DEFAULT 'ping' AFTER target_user_id`},
		{name: "session_key", sql: `ALTER TABLE no_tap_peer_probes ADD COLUMN session_key VARCHAR(128) NULL AFTER purpose`},
	} {
		exists, err := columnExists(ctx, db, "no_tap_peer_probes", field.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := db.ExecContext(ctx, field.sql); err != nil {
				return fmt.Errorf("add no-TAP peer probe %s: %w", field.name, err)
			}
		}
	}
	return nil
}

func runMigration(ctx context.Context, db *sql.DB, version string, migrate func(context.Context, *sql.DB) error) error {
	var applied int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM platform_schema_migrations WHERE version = ?", version).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %s: %w", version, err)
	}
	if applied > 0 {
		return nil
	}

	if err := migrate(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "INSERT IGNORE INTO platform_schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	return nil
}

func migrateSoccerIdentity(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS platform_users (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			soccer_user_id BIGINT UNSIGNED NULL,
			username_snapshot VARCHAR(255) NOT NULL,
			nickname_snapshot VARCHAR(255) NOT NULL,
			status ENUM('active', 'disabled') NOT NULL DEFAULT 'active',
			last_login_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY platform_users_soccer_user_id_unique (soccer_user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create platform users table: %w", err)
	}

	legacyUsers, err := tableExists(ctx, db, "users")
	if err != nil {
		return err
	}
	if legacyUsers {
		if _, err := db.ExecContext(ctx, `
			INSERT IGNORE INTO platform_users
				(id, soccer_user_id, username_snapshot, nickname_snapshot, status, created_at, updated_at)
			SELECT id, NULL, username, nickname, 'disabled', created_at, updated_at
			FROM users`); err != nil {
			return fmt.Errorf("preserve legacy platform users: %w", err)
		}
	}

	constraints, err := userForeignKeys(ctx, db)
	if err != nil {
		return err
	}
	hasPlatformForeignKey := false
	for _, constraint := range constraints {
		if constraint.referencedTable == "platform_users" {
			hasPlatformForeignKey = true
			continue
		}
		if !safeIdentifier.MatchString(constraint.name) {
			return fmt.Errorf("unsafe room lease foreign key name %q", constraint.name)
		}
		query := fmt.Sprintf("ALTER TABLE room_ip_leases DROP FOREIGN KEY `%s`", constraint.name)
		if _, err := db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("drop legacy room lease foreign key: %w", err)
		}
	}
	if !hasPlatformForeignKey {
		if _, err := db.ExecContext(ctx, `
			ALTER TABLE room_ip_leases
			ADD CONSTRAINT room_ip_leases_platform_user_id_foreign
			FOREIGN KEY (user_id) REFERENCES platform_users (id)`); err != nil {
			return fmt.Errorf("add platform user room lease foreign key: %w", err)
		}
	}
	return nil
}

func migrateLimitRoomsToSix(ctx context.Context, db *sql.DB) error {
	hasSortOrder, err := columnExists(ctx, db, "rooms", "sort_order")
	if err != nil {
		return err
	}
	if !hasSortOrder {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		DELETE room_ip_leases
		FROM room_ip_leases
		INNER JOIN rooms ON rooms.id = room_ip_leases.room_id
		WHERE rooms.sort_order > 6`); err != nil {
		return fmt.Errorf("delete leases for hidden rooms: %w", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM rooms WHERE sort_order > 6"); err != nil {
		return fmt.Errorf("delete hidden rooms: %w", err)
	}
	return nil
}

func migrateSingleActiveSession(ctx context.Context, db *sql.DB) error {
	hasActiveSession, err := columnExists(ctx, db, "platform_users", "active_session_id")
	if err != nil {
		return err
	}
	if !hasActiveSession {
		if _, err := db.ExecContext(ctx, `
			ALTER TABLE platform_users
			ADD COLUMN active_session_id VARCHAR(43) NULL AFTER last_login_at`); err != nil {
			return fmt.Errorf("add platform user active session: %w", err)
		}
	}

	hasLeaseSession, err := columnExists(ctx, db, "room_ip_leases", "session_id")
	if err != nil {
		return err
	}
	if !hasLeaseSession {
		if _, err := db.ExecContext(ctx, `
			ALTER TABLE room_ip_leases
			ADD COLUMN session_id VARCHAR(43) NULL AFTER user_id`); err != nil {
			return fmt.Errorf("add room lease session: %w", err)
		}
	}

	hasCredentialExpiry, err := columnExists(ctx, db, "room_ip_leases", "credential_expires_at")
	if err != nil {
		return err
	}
	if hasCredentialExpiry {
		// Tokens issued before this migration have no session ID. Expire their leases
		// so the normal reaper clears stale room allocations.
		if _, err := db.ExecContext(ctx, `
			UPDATE room_ip_leases
			SET credential_expires_at = CURRENT_TIMESTAMP
			WHERE session_id IS NULL`); err != nil {
			return fmt.Errorf("expire legacy room leases: %w", err)
		}
	}
	return nil
}

func migrateVpnUsername(ctx context.Context, db *sql.DB) error {
	hasLegacyColumn, err := columnExists(ctx, db, "room_ip_leases", "softether_username")
	if err != nil {
		return err
	}
	if !hasLegacyColumn {
		return nil
	}
	hasVpnColumn, err := columnExists(ctx, db, "room_ip_leases", "vpn_username")
	if err != nil {
		return err
	}
	if hasVpnColumn {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE room_ip_leases
		CHANGE COLUMN softether_username vpn_username VARCHAR(96) NOT NULL`); err != nil {
		return fmt.Errorf("rename room lease username column: %w", err)
	}
	return nil
}

func migrateRoomRealIP(ctx context.Context, db *sql.DB) error {
	hasColumn, err := columnExists(ctx, db, "room_ip_leases", "real_ip")
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE room_ip_leases
		ADD COLUMN real_ip VARCHAR(45) NULL AFTER vpn_username`); err != nil {
		return fmt.Errorf("add room lease real ip column: %w", err)
	}
	return nil
}

func migrateRoomSubnet222(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		DELETE FROM room_ip_leases
		WHERE room_id IN (
			SELECT id FROM rooms WHERE sort_order BETWEEN 1 AND 6
		)`); err != nil {
		return fmt.Errorf("clear legacy room leases before subnet move: %w", err)
	}

	for room := 1; room <= 6; room++ {
		subnet := fmt.Sprintf("10.222.%d.0/24", room)
		start := fmt.Sprintf("10.222.%d.10", room)
		end := fmt.Sprintf("10.222.%d.109", room)
		if _, err := db.ExecContext(ctx, `
			UPDATE rooms
			SET subnet_cidr = ?, ip_start = ?, ip_end = ?
			WHERE sort_order = ?`, subnet, start, end, room); err != nil {
			return fmt.Errorf("update room %d subnet: %w", room, err)
		}
	}
	return nil
}

func migrateDynamicOpenVPNIP(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		UPDATE room_ip_leases
		SET virtual_ip = NULL
		WHERE released_at IS NULL`); err != nil {
		return fmt.Errorf("clear active room lease ips before dynamic openvpn migration: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE room_ip_leases
		MODIFY COLUMN virtual_ip VARCHAR(15) NULL`); err != nil {
		return fmt.Errorf("allow dynamic room lease ip assignment: %w", err)
	}
	return nil
}

func migrateN2NStaticRoomIP(ctx context.Context, db *sql.DB) error {
	// n2n receives the room address from the API before edge starts. A lease
	// without an address is an old OpenVPN lease and cannot safely participate.
	if _, err := db.ExecContext(ctx, "DELETE FROM room_ip_leases WHERE virtual_ip IS NULL"); err != nil {
		return fmt.Errorf("clear dynamic room leases before n2n static allocation: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE room_ip_leases
		MODIFY COLUMN virtual_ip VARCHAR(15) NOT NULL`); err != nil {
		return fmt.Errorf("require static room lease ip for n2n: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check table %s: %w", table, err)
	}
	return count > 0, nil
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`, table, column).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check column %s.%s: %w", table, column, err)
	}
	return count > 0, nil
}

type foreignKey struct {
	name, referencedTable string
}

func userForeignKeys(ctx context.Context, db *sql.DB) ([]foreignKey, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT CONSTRAINT_NAME, REFERENCED_TABLE_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME = 'room_ip_leases'
			AND COLUMN_NAME = 'user_id'
			AND REFERENCED_TABLE_NAME IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("find room lease user foreign keys: %w", err)
	}
	defer rows.Close()

	constraints := make([]foreignKey, 0)
	for rows.Next() {
		var constraint foreignKey
		if err := rows.Scan(&constraint.name, &constraint.referencedTable); err != nil {
			return nil, fmt.Errorf("scan room lease user foreign key: %w", err)
		}
		constraints = append(constraints, constraint)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read room lease user foreign keys: %w", err)
	}
	return constraints, nil
}
