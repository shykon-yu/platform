package platformdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func TestMigrateLegacySchema(t *testing.T) {
	dsn := os.Getenv("PLATFORM_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set PLATFORM_MIGRATION_TEST_DSN to run the MySQL migration test")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var databaseName string
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&databaseName); err != nil {
		t.Fatalf("read database name: %v", err)
	}
	if !strings.HasSuffix(databaseName, "_migration_test") {
		t.Fatalf("refusing to reset non-test database %q", databaseName)
	}

	statements := []string{
		"SET FOREIGN_KEY_CHECKS = 0",
		"DROP TABLE IF EXISTS no_tap_peer_probes, no_tap_room_leases, no_tap_rooms, room_ip_leases, rooms, platform_users, users, platform_schema_migrations",
		"SET FOREIGN_KEY_CHECKS = 1",
		`CREATE TABLE users (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(32) NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			nickname VARCHAR(32) NOT NULL,
			status ENUM('active', 'disabled') NOT NULL DEFAULT 'active',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB`,
		`CREATE TABLE rooms (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			code VARCHAR(32) NOT NULL,
			name VARCHAR(64) NOT NULL,
			region VARCHAR(32) NOT NULL,
			hub_name VARCHAR(64) NOT NULL,
			subnet_cidr VARCHAR(32) NOT NULL,
			ip_start VARCHAR(15) NOT NULL,
			ip_end VARCHAR(15) NOT NULL,
			capacity SMALLINT UNSIGNED NOT NULL DEFAULT 100,
			status ENUM('open', 'maintenance', 'closed') NOT NULL DEFAULT 'open',
			sort_order INT NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB`,
		`CREATE TABLE room_ip_leases (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			room_id BIGINT UNSIGNED NOT NULL,
			user_id BIGINT UNSIGNED NOT NULL,
			virtual_ip VARCHAR(15) NOT NULL,
			state ENUM('allocated', 'connected', 'released') NOT NULL DEFAULT 'allocated',
			softether_username VARCHAR(96) NOT NULL,
			credential_expires_at DATETIME NOT NULL,
			released_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			CONSTRAINT room_ip_leases_room_id_foreign FOREIGN KEY (room_id) REFERENCES rooms (id),
			CONSTRAINT room_ip_leases_user_id_foreign FOREIGN KEY (user_id) REFERENCES users (id)
		) ENGINE=InnoDB`,
		"INSERT INTO users (id, username, password_hash, nickname) VALUES (7, 'legacy', 'unused', 'Legacy')",
		"INSERT INTO rooms (id, code, name, region, hub_name, subnet_cidr, ip_start, ip_end, sort_order) VALUES (1, 'room-01', '对战房间 01', '主节点', 'we8-room-01', '10.80.1.0/24', '10.80.1.10', '10.80.1.109', 1)",
		"INSERT INTO room_ip_leases (room_id, user_id, virtual_ip, softether_username, credential_expires_at) VALUES (1, 7, '10.80.1.10', 'legacy-user', CURRENT_TIMESTAMP)",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare legacy schema with %q: %v", statement, err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	var soccerUserID sql.NullInt64
	var status string
	if err := db.QueryRowContext(ctx, "SELECT soccer_user_id, status FROM platform_users WHERE id = 7").Scan(&soccerUserID, &status); err != nil {
		t.Fatalf("read migrated user: %v", err)
	}
	if soccerUserID.Valid || status != "disabled" {
		t.Fatalf("migrated user soccer_user_id=%v status=%q", soccerUserID, status)
	}

	var referencedTable string
	if err := db.QueryRowContext(ctx, `
		SELECT REFERENCED_TABLE_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME = 'room_ip_leases'
			AND COLUMN_NAME = 'user_id'
			AND REFERENCED_TABLE_NAME IS NOT NULL`).Scan(&referencedTable); err != nil {
		t.Fatalf("read migrated foreign key: %v", err)
	}
	if referencedTable != "platform_users" {
		t.Fatalf("room_ip_leases.user_id references %q", referencedTable)
	}

	for table, column := range map[string]string{
		"platform_users": "active_session_id",
		"room_ip_leases": "session_id",
	} {
		exists, err := columnExists(ctx, db, table, column)
		if err != nil {
			t.Fatalf("check %s.%s: %v", table, column, err)
		}
		if !exists {
			t.Fatalf("migration did not add %s.%s", table, column)
		}
	}

	rows, err := db.QueryContext(ctx, "SELECT id, subnet_cidr, ip_start, ip_end FROM no_tap_rooms ORDER BY id")
	if err != nil {
		t.Fatalf("read No-TAP rooms: %v", err)
	}
	defer rows.Close()
	roomCount := 0
	for rows.Next() {
		roomCount++
		var id int
		var subnet, start, end string
		if err := rows.Scan(&id, &subnet, &start, &end); err != nil {
			t.Fatalf("scan No-TAP room: %v", err)
		}
		wantSubnet := fmt.Sprintf("10.122.%d.0/24", id)
		wantStart := fmt.Sprintf("10.122.%d.10", id)
		wantEnd := fmt.Sprintf("10.122.%d.109", id)
		if id < 1 || id > 4 || subnet != wantSubnet || start != wantStart || end != wantEnd {
			t.Fatalf("No-TAP room %d = %q %q-%q", id, subnet, start, end)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate No-TAP rooms: %v", err)
	}
	if roomCount != 4 {
		t.Fatalf("No-TAP room count = %d, want 4", roomCount)
	}

	for _, item := range []struct{ table, column string }{
		{"no_tap_peer_probes", "requester_description"},
		{"no_tap_peer_probes", "target_description"},
	} {
		table, column := item.table, item.column
		exists, err := columnExists(ctx, db, table, column)
		if err != nil {
			t.Fatalf("check %s.%s: %v", table, column, err)
		}
		if !exists {
			t.Fatalf("migration did not add %s.%s", table, column)
		}
	}
}
