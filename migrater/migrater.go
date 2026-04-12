// Package migrater applies numbered SQL migration files to a [database/sql.DB].
//
// Migration files are read from an [fs.FS] (or an OS directory) and applied in
// lexicographic order. Each file is executed inside its own transaction; on
// success the migration ID is recorded in a state table so that subsequent
// calls to [Migrater.Run] are idempotent.
//
// # Migration file naming
//
// Files must have a .sql extension and begin with a zero-padded numeric prefix:
//
//	001.sql
//	002-create-users.sql
//	003-add-index.sql
//
// The migration ID stored in the state table is the leading numeric prefix only
// (e.g. "001", "003") — the description suffix is ignored.
//
// # Usage (embedded migrations)
//
//	//go:embed migrations
//	var migrationsFS embed.FS
//
//	db, _ := sql.Open("sqlite3", "app.db")
//	m := migrater.New(db, migrater.WithFS(migrationsFS, "migrations"))
//	if err := m.Run(context.Background()); err != nil {
//	    log.Fatal(err)
//	}
//
// # Usage (OS directory)
//
//	m := migrater.New(db,
//	    migrater.WithDir("./migrations"),
//	    migrater.WithPostgres(),
//	    migrater.WithInfoLog(func(f string, v ...any) { log.Printf(f, v...) }),
//	)
//	if err := m.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
package migrater

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Migrater applies SQL migration files to a database in lexicographic order,
// recording each applied migration in a state table to guarantee idempotency.
//
// Always construct a Migrater via [New]; the zero value is not usable.
type Migrater struct {
	db         *sql.DB
	fsys       fs.FS
	dir        string
	table      string
	info       LogFunc
	debug      LogFunc
	recoverLog LogFunc
	postgres   bool
}

// New creates a Migrater for the given database and applies the provided options.
//
// Default settings:
//   - migrations directory: "./migrations" (OS filesystem)
//   - state table: "migrater_state"
//   - info log: [DefaultInfoLog] (stdout with [INFO] prefix)
//   - debug log: nil (silent)
//   - recover log: [DefaultRecoverLog] (stdout with [RECOVER] prefix)
//   - placeholder style: ? (SQLite); use [WithPostgres] for PostgreSQL
func New(db *sql.DB, options ...Option) *Migrater {
	m := &Migrater{
		db:         db,
		table:      DefaultTableName,
		info:       DefaultInfoLog,
		debug:      DefaultDebugLog,
		recoverLog: DefaultRecoverLog,
	}

	// Apply WithDir default before user options so WithFS can override it.
	WithDir(DefaultDir)(m)

	for _, opt := range options {
		opt(m)
	}

	return m
}

// Run applies all pending migration files in lexicographic order.
//
// Run is safe to call multiple times; migrations already recorded in the state
// table are skipped. All applied IDs are loaded with a single SELECT at startup —
// there is no per-migration round-trip to the state table.
//
// Each migration runs in its own transaction. If a migration fails the
// transaction is rolled back and Run returns immediately with the error;
// subsequent migrations are not attempted.
//
// Run is panic-safe: any panic originating from migration SQL execution is
// recovered, logged via the recover log function, and returned as an error.
//
// Canceling ctx causes the next database call to fail; any in-flight migration
// transaction is rolled back before Run returns.
func (m *Migrater) Run(ctx context.Context) (err error) {
	if m.table == "" {
		return fmt.Errorf("migrater: table name must not be empty (check WithTableName option)")
	}

	defer func() {
		if r := recover(); r != nil {
			m.logf(m.recoverLog, "panic recovered: %v", r)
			err = fmt.Errorf("migrater panic: %v", r)
		}
	}()

	if err = m.ensureTable(ctx); err != nil {
		return fmt.Errorf("migrater: ensure state table: %w", err)
	}

	applied, err := m.loadApplied(ctx)
	if err != nil {
		return fmt.Errorf("migrater: load applied migrations: %w", err)
	}

	files, err := m.collectFiles()
	if err != nil {
		return fmt.Errorf("migrater: %w", err)
	}

	for _, f := range files {
		if err := m.applyFile(ctx, f, applied); err != nil {
			return err
		}
	}

	return nil
}

// collectFiles reads the migrations directory and returns .sql entries sorted
// lexicographically. Returns an error if the directory cannot be read.
func (m *Migrater) collectFiles() ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(m.fsys, m.dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %q: %w", m.dir, err)
	}

	var files []fs.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	return files, nil
}

// applyFile processes a single migration entry: skips it if already applied or
// has no numeric prefix, otherwise reads and applies it.
func (m *Migrater) applyFile(ctx context.Context, f fs.DirEntry, applied map[string]bool) error {
	id := migrationID(f.Name())
	if id == "" {
		m.logf(m.debug, "skip %s: no numeric prefix", f.Name())
		return nil
	}

	if applied[id] {
		m.logf(m.debug, "skip migration %s (already applied)", id)
		return nil
	}

	contentb, err := fs.ReadFile(m.fsys, path.Join(m.dir, f.Name()))
	if err != nil {
		return fmt.Errorf("migrater: read %s: %w", f.Name(), err)
	}
	content := string(contentb)

	m.logf(m.info, "applying migration %s", id)
	m.logf(m.debug, "%s", content)
	if err := m.apply(ctx, id, content); err != nil {
		return fmt.Errorf("migrater: apply %s: %w", id, err)
	}
	m.logf(m.info, "migration %s applied", id)

	return nil
}

// ensureTable creates the migration state table if it does not already exist.
func (m *Migrater) ensureTable(ctx context.Context) error {
	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (`+
			`id TEXT PRIMARY KEY, `+
			`applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP`+
			`)`,
		m.table,
	)
	m.logf(m.debug, "ensure table: %s", query)
	_, err := m.db.ExecContext(ctx, query)
	return err
}

// loadApplied performs a single SELECT to retrieve all already-applied
// migration IDs, returning them as a set (map[id]true).
func (m *Migrater) loadApplied(ctx context.Context) (map[string]bool, error) {
	query := fmt.Sprintf("SELECT id FROM %s", m.table) //nolint:gosec // table name is a developer-set option, not user input
	m.logf(m.debug, "load applied: %s", query)

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		applied[id] = true
	}
	return applied, rows.Err()
}

// apply executes a single migration inside a transaction, then records the
// migration ID in the state table. The transaction is rolled back on any error.
func (m *Migrater) apply(ctx context.Context, id, content string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, content); err != nil {
		_ = tx.Rollback() //nolint:errcheck // best-effort; primary error is already returned
		return err
	}

	insertQuery := fmt.Sprintf("INSERT INTO %s (id) VALUES (%s)", m.table, m.placeholder(1)) //nolint:gosec // table name is a developer-set option, not user input
	m.logf(m.debug, "record state: %s [%s]", insertQuery, id)
	if _, err := tx.ExecContext(ctx, insertQuery, id); err != nil {
		_ = tx.Rollback() //nolint:errcheck // best-effort; primary error is already returned
		return err
	}

	return tx.Commit()
}

// placeholder returns the SQL parameter placeholder for position n.
// It returns "?" for SQLite (the default) and "$N" for PostgreSQL.
func (m *Migrater) placeholder(n int) string {
	if m.postgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// migrationID extracts the leading numeric prefix from a migration filename.
// For example: "001.sql" → "001", "003-some-desc.sql" → "003", "readme.sql" → "".
func migrationID(filename string) string {
	end := 0
	for end < len(filename) && filename[end] >= '0' && filename[end] <= '9' {
		end++
	}
	return filename[:end]
}

// logf calls fn with the formatted message if fn is non-nil.
func (m *Migrater) logf(fn LogFunc, format string, v ...any) {
	if fn != nil {
		fn(format, v...)
	}
}
