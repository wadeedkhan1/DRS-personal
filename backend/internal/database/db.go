package database

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"drs/backend/internal/config"
	"drs/backend/migrations"

	"golang.org/x/crypto/bcrypt"

	_ "github.com/lib/pq"
)

// DB wraps the connection pool.
type DB struct {
	*sql.DB
}

// InitDB connects, migrates, and seeds.
func InitDB(cfg *config.Config) (*DB, error) {
	ensureDatabaseExists(cfg)

	db, err := sql.Open("postgres", cfg.DatabaseURL())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		// Spell out the fix. This is the single most common first-run failure, and the
		// bare driver error ("password authentication failed") does not say where the
		// wrong password came from.
		return nil, fmt.Errorf("could not connect to PostgreSQL at %s:%s as user %q: %w\n"+
			"  Check DB_HOST, DB_PORT, DB_USER and DB_PASSWORD in backend/.env — "+
			"DB_PASSWORD must match the password for this PostgreSQL server",
			cfg.DBHost, cfg.DBPort, cfg.DBUser, err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	if err := seedDefaults(db, cfg); err != nil {
		log.Printf("[DB WARN] Failed to seed defaults: %v", err)
	}

	log.Printf("[DB] Connected to PostgreSQL and schema is up to date.")
	return &DB{db}, nil
}

// migrate applies every embedded *.up.sql that has not run yet, in filename order.
//
// Each migration runs inside its own transaction together with the row that records
// it, so a migration either applies completely and is marked, or does neither. A
// half-applied schema with a version claiming success is the failure mode worth the
// most effort to avoid.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	// Filename order is the migration order, which is why they are zero-padded.
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".up.sql")
		if applied[version] {
			continue
		}

		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
		log.Printf("[DB] Applied migration %s", version)
	}
	return nil
}

// ensureDatabaseExists creates the target database if it is missing, which makes a
// fresh local checkout work without manual setup.
//
// Failure here is not fatal — in Docker the database is created by the Postgres image
// and this connection may not be permitted at all — but it is logged. Silently
// swallowing the error meant a wrong password produced no database and no explanation,
// leaving "the database isn't there" as the only visible symptom.
func ensureDatabaseExists(cfg *config.Config) {
	adminConnStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=postgres sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBSSLMode)

	adminDB, err := sql.Open("postgres", adminConnStr)
	if err != nil {
		log.Printf("[DB WARN] Could not open a connection to check whether %q exists: %v", cfg.DBName, err)
		return
	}
	defer adminDB.Close()

	var exists bool
	if err := adminDB.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, cfg.DBName,
	).Scan(&exists); err != nil {
		log.Printf("[DB WARN] Could not check whether database %q exists: %v", cfg.DBName, err)
		return
	}
	if exists {
		return
	}

	// CREATE DATABASE cannot be parameterised, so the name is quoted as an
	// identifier instead. It comes from our own config rather than a request, but
	// building DDL by concatenation is a habit worth not having.
	log.Printf("[DB] Database %q does not exist. Creating it now...", cfg.DBName)
	if _, err := adminDB.Exec(fmt.Sprintf("CREATE DATABASE %s", quoteIdentifier(cfg.DBName))); err != nil {
		log.Printf("[DB WARN] Could not create database %q: %v", cfg.DBName, err)
	}
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// seedDefaults creates the organization and the first super admin, once.
func seedDefaults(db *sql.DB, cfg *config.Config) error {
	var orgID string
	err := db.QueryRow("SELECT id FROM organizations LIMIT 1").Scan(&orgID)
	if err == sql.ErrNoRows {
		if err := db.QueryRow(
			"INSERT INTO organizations (name) VALUES ('DRS Primary Organization') RETURNING id",
		).Scan(&orgID); err != nil {
			return fmt.Errorf("failed to insert default organization: %w", err)
		}
		log.Printf("[DB] Created default organization: %s", orgID)
	} else if err != nil {
		return err
	}

	var superAdmins int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE role = 'super_admin'").Scan(&superAdmins); err != nil {
		return err
	}
	if superAdmins > 0 {
		return nil
	}

	// No fallback password. If DEFAULT_SUPERADMIN_PASSWORD is unset there is simply no
	// account, and the operator is told why: a baked-in default would be a publicly
	// known credential on every deployment that forgot to override it.
	if strings.TrimSpace(cfg.AdminPass) == "" {
		log.Printf("[DB WARN] No super admin exists and DEFAULT_SUPERADMIN_PASSWORD is unset — " +
			"set it and restart to create the first account")
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`
		INSERT INTO users (org_id, email, password_hash, role)
		VALUES ($1, LOWER($2), $3, 'super_admin')
	`, orgID, cfg.AdminEmail, string(hash)); err != nil {
		return fmt.Errorf("failed to create default super admin: %w", err)
	}
	log.Printf("[DB] Seeded initial Super Admin account: %s", cfg.AdminEmail)
	return nil
}
