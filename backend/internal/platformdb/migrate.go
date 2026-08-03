package platformdb

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
)

const soccerIdentityMigration = "20260803_soccer_identity"

var safeIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS platform_schema_migrations (
			version VARCHAR(64) NOT NULL PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
		return fmt.Errorf("create platform migrations table: %w", err)
	}

	var applied int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM platform_schema_migrations WHERE version = ?", soccerIdentityMigration).Scan(&applied); err != nil {
		return fmt.Errorf("check soccer identity migration: %w", err)
	}
	if applied > 0 {
		return nil
	}

	if err := migrateSoccerIdentity(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "INSERT IGNORE INTO platform_schema_migrations (version) VALUES (?)", soccerIdentityMigration); err != nil {
		return fmt.Errorf("record soccer identity migration: %w", err)
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
