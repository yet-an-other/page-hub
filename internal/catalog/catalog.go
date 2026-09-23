// Package catalog owns the private SQLite catalog: numbered migrations, the
// explicit migration command, runtime schema verification, and the catalog
// read/write operations used by the application. The catalog, not storage,
// defines managed Publications (ADR-0001).
package catalog

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migration is one numbered, checksummed schema step.
type Migration struct {
	Version  int
	Name     string
	Checksum string
	SQL      string
}

// Migrations returns the embedded migrations ordered by version.
func Migrations() ([]Migration, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := entry.Name()
		versionPart := strings.SplitN(name, "_", 2)[0]
		version, err := strconv.Atoi(versionPart)
		if err != nil {
			return nil, fmt.Errorf("migration file %q must start with a numeric version", name)
		}
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		sum := sha256.Sum256(content)
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			Checksum: hex.EncodeToString(sum[:]),
			SQL:      string(content),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

// LatestVersion returns the newest embedded schema version.
func LatestVersion() (int, error) {
	migrations, err := Migrations()
	if err != nil {
		return 0, err
	}
	if len(migrations) == 0 {
		return 0, fmt.Errorf("no embedded migrations found")
	}
	return migrations[len(migrations)-1].Version, nil
}

// SupportedSchemaRange describes the catalog schema versions this binary
// accepts: currently exactly the latest version.
func SupportedSchemaRange() string {
	latest, err := LatestVersion()
	if err != nil {
		return "unknown"
	}
	return strconv.Itoa(latest)
}

// openDB opens the SQLite catalog with foreign keys enforced and a bounded
// busy timeout. The pure-Go driver keeps the release build CGO-free.
func openDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": {
			"foreign_keys(1)",
			"busy_timeout(5000)",
			"journal_mode(WAL)",
			"synchronous(NORMAL)",
		},
	}.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open catalog: %w", err)
	}
	// The pure-Go driver serializes access safely; a single connection keeps
	// BEGIN IMMEDIATE transactions exclusive within this process.
	db.SetMaxOpenConns(1)
	return db, nil
}

// Migrate applies every pending embedded migration in order. It refuses to
// migrate a catalog that a runtime currently holds, that is newer than the
// embedded schema, or whose recorded history does not match the embedded
// checksums. Migrations never run automatically.
func Migrate(path string) (from int, to int, err error) {
	lock, err := AcquireLock(path)
	if err != nil {
		return 0, 0, err
	}
	defer lock.Release()

	migrations, err := Migrations()
	if err != nil {
		return 0, 0, err
	}
	db, err := openDB(path)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()

	applied, err := readAppliedMigrations(db)
	if err != nil {
		return 0, 0, err
	}
	from = len(applied)
	if err := verifyKnownHistory(applied, migrations); err != nil {
		return from, 0, err
	}

	for _, migration := range migrations {
		if _, done := applied[migration.Version]; done {
			continue
		}
		if err := applyMigration(db, migration); err != nil {
			return from, 0, fmt.Errorf("apply migration %03d (%s): %w", migration.Version, migration.Name, err)
		}
		slog.Info("catalog migration applied", "version", migration.Version, "name", migration.Name)
	}
	return from, len(migrations), nil
}

type appliedMigration struct {
	Version  int
	Checksum string
}

func readAppliedMigrations(db *sql.DB) (map[int]appliedMigration, error) {
	rows, err := db.Query(`SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		// A fresh catalog has no schema_migrations table yet.
		if strings.Contains(err.Error(), "no such table") {
			return map[int]appliedMigration{}, nil
		}
		return nil, fmt.Errorf("read schema history: %w", err)
	}
	defer rows.Close()
	applied := map[int]appliedMigration{}
	for rows.Next() {
		var record appliedMigration
		if err := rows.Scan(&record.Version, &record.Checksum); err != nil {
			return nil, fmt.Errorf("read schema history: %w", err)
		}
		applied[record.Version] = record
	}
	return applied, rows.Err()
}

// verifyKnownHistory refuses catalogs whose recorded history is not exactly a
// prefix of the embedded migrations: tampered checksums, partially applied
// steps, or unknown newer migrations.
func verifyKnownHistory(applied map[int]appliedMigration, migrations []Migration) error {
	byVersion := map[int]Migration{}
	for _, migration := range migrations {
		byVersion[migration.Version] = migration
	}
	for version, record := range applied {
		migration, known := byVersion[version]
		if !known {
			return fmt.Errorf("catalog schema version %d is newer than this binary supports (compatible range: %s)", version, SupportedSchemaRange())
		}
		if record.Checksum != migration.Checksum {
			return fmt.Errorf("catalog schema migration %03d does not match this binary's checksum", version)
		}
	}
	// Contiguity: applied versions must be exactly 1..N.
	for i := 1; i <= len(applied); i++ {
		if _, ok := applied[i]; !ok {
			return fmt.Errorf("catalog schema is partially applied: migration %03d is missing between applied migrations", i)
		}
	}
	return nil
}

func applyMigration(db *sql.DB, migration Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(migration.SQL); err != nil {
		return err
	}
	// The migration file itself creates schema_migrations on first run; record
	// history inside the same transaction so a crash cannot leave a partially
	// applied schema.
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
		migration.Version, migration.Name, migration.Checksum, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, migration.Version)); err != nil {
		return err
	}
	return tx.Commit()
}

// Store is an opened catalog connection for the runtime. The store refuses to
// open against an incompatible, partially applied, or unmigrated schema.
type Store struct {
	db *sql.DB
}

// OpenRuntime opens the catalog for the running manager. It verifies the
// schema history and integrity but never migrates.
func OpenRuntime(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("catalog path must not be empty")
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.verifySchema(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) verifySchema() error {
	var userVersion int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		return fmt.Errorf("read catalog schema version: %w", err)
	}
	latest, err := LatestVersion()
	if err != nil {
		return err
	}
	applied, err := readAppliedMigrations(s.db)
	if err != nil {
		return err
	}
	if userVersion == 0 && len(applied) == 0 {
		return fmt.Errorf("catalog is not initialized; run the migration command first (compatible schema: %s)", SupportedSchemaRange())
	}
	if userVersion != latest || len(applied) != len(mustMigrations()) {
		// Distinguish a partially applied schema from an older or newer one.
		if err := verifyKnownHistory(applied, mustMigrations()); err != nil {
			return err
		}
		return fmt.Errorf("catalog schema version %d does not match this binary (compatible schema: %s); run the migration command", userVersion, SupportedSchemaRange())
	}
	if err := verifyKnownHistory(applied, mustMigrations()); err != nil {
		return err
	}
	var check string
	if err := s.db.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("catalog integrity check failed")
	}
	return nil
}

func mustMigrations() []Migration {
	migrations, err := Migrations()
	if err != nil {
		panic(err)
	}
	return migrations
}

// Close closes the catalog connection.
func (s *Store) Close() error { return s.db.Close() }

// LockPath returns the sidecar lock file guarding exclusive catalog access.
func LockPath(catalogPath string) string {
	return catalogPath + ".lock"
}

// AcquireLock takes the exclusive catalog lock, failing immediately when the
// runtime or another administrative command holds it.
func AcquireLock(catalogPath string) (*Lock, error) {
	if err := ensureParentDir(catalogPath); err != nil {
		return nil, err
	}
	return acquireFlock(LockPath(catalogPath))
}

func ensureParentDir(catalogPath string) error {
	dir := filepath.Dir(catalogPath)
	if dir == "" || dir == "." {
		return nil
	}
	return mkdirAll(dir)
}
