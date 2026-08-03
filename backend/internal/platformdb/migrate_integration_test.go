package platformdb

import (
	"context"
	"database/sql"
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
		"DROP TABLE IF EXISTS room_ip_leases, rooms, platform_users, users, platform_schema_migrations",
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
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY
		) ENGINE=InnoDB`,
		`CREATE TABLE room_ip_leases (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			room_id BIGINT UNSIGNED NOT NULL,
			user_id BIGINT UNSIGNED NOT NULL,
			CONSTRAINT room_ip_leases_room_id_foreign FOREIGN KEY (room_id) REFERENCES rooms (id),
			CONSTRAINT room_ip_leases_user_id_foreign FOREIGN KEY (user_id) REFERENCES users (id)
		) ENGINE=InnoDB`,
		"INSERT INTO users (id, username, password_hash, nickname) VALUES (7, 'legacy', 'unused', 'Legacy')",
		"INSERT INTO rooms (id) VALUES (1)",
		"INSERT INTO room_ip_leases (room_id, user_id) VALUES (1, 7)",
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
}
