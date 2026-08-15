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
	return nil
}

func migrateNoTapRooms(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS no_tap_rooms (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
			code VARCHAR(32) NOT NULL,
			name VARCHAR(64) NOT NULL,
			region VARCHAR(32) NOT NULL,
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
	for index := 1; index <= 3; index++ {
		code := fmt.Sprintf("notap-%02d", index)
		name := fmt.Sprintf("无网卡房间 %02d", index)
		subnet := fmt.Sprintf("10.122.%d.0/24", index)
		start := fmt.Sprintf("10.122.%d.10", index)
		end := fmt.Sprintf("10.122.%d.109", index)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO no_tap_rooms (id, code, name, region, subnet_cidr, ip_start, ip_end, capacity, sort_order)
			VALUES (?, ?, ?, '无网卡中继', ?, ?, ?, 100, ?)
			ON DUPLICATE KEY UPDATE name = VALUES(name), subnet_cidr = VALUES(subnet_cidr), ip_start = VALUES(ip_start), ip_end = VALUES(ip_end), sort_order = VALUES(sort_order)`, index, code, name, subnet, start, end, index); err != nil {
			return fmt.Errorf("seed no-TAP room %d: %w", index, err)
		}
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
