// Package migrater applies numbered SQL migration files to a [database/sql.DB].
//
// Migration files are read from an [fs.FS] (or an OS directory) and applied in
// numeric order of their leading prefix. Each file is executed inside its own
// transaction; on success the migration ID is recorded in a state table so
// that subsequent calls to [Migrater.Run] are idempotent.
//
// # Migration file naming
//
// Files must have a .sql extension and begin with a numeric prefix. The
// numeric prefix determines apply order, so zero-padding is optional but
// recommended for readable directory listings:
//
//	001.sql
//	002-create-users.sql
//	003-add-index.sql
//
// The migration ID stored in the state table is the leading numeric prefix
// only (e.g. "001", "003") — the description suffix is ignored. Two files
// sharing the same numeric prefix (e.g. 001.sql and 001-foo.sql) cause
// [Migrater.Run] to return an error before any migration is applied.
//
// # Multi-statement migrations
//
// Each migration file is executed as a single ExecContext call. Whether a
// file may contain multiple SQL statements depends on the database driver:
// SQLite (mattn/go-sqlite3, modernc.org/sqlite), lib/pq, and jackc/pgx accept
// multi-statement input by default; go-sql-driver/mysql requires
// "?multiStatements=true" in the DSN.
//
// # Drift detection
//
// On apply, migrater records a SHA-256 hash of each migration's content in
// the state table's content_hash column. On subsequent runs, when an
// already-applied migration is encountered, its current file content is
// re-hashed and compared against the stored hash; a mismatch is logged via
// the info log to surface that the file has been modified after being
// applied (database state and source files have diverged). Drift is
// informational only — it does not block subsequent migrations or cause
// [Migrater.Run] to error.
//
// State tables created by versions of this package that did not include the
// content_hash column are migrated automatically (ALTER TABLE ADD COLUMN).
// Existing rows have NULL content_hash; drift checks are skipped for those
// rows because there is no recorded hash to compare against.
//
// The hash is computed over the raw bytes of each file. Files with different
// line-ending conventions (CRLF vs LF) produce different hashes. When using
// [WithDir] across mixed-platform hosts, ensure consistent line endings —
// e.g. a `.gitattributes` entry such as `*.sql text eol=lf` — so the hash
// recorded by one host matches the hash computed by another. Embedded
// migrations via `//go:embed` are not affected (Go embeds raw file bytes
// without conversion).
//
// # Forward-only design
//
// migrater applies migrations in numeric order and records each as applied;
// it does not support down/rollback migrations or version pinning. To revert
// a change, write a new forward migration that undoes it.
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
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// tableNameRe matches simple SQL identifiers: a leading ASCII letter or
// underscore followed by ASCII letters, digits, or underscores. The
// state-table name is interpolated into SQL with fmt.Sprintf, so this guards
// developer-set values passed via [WithTableName] from accidentally
// introducing injection vectors.
//
// Schema-qualified names (e.g. "public.migrations") are not supported —
// the dot is rejected. Non-ASCII letters are also rejected; pick an
// ASCII-only name for the state table.
var tableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// tableNameMaxLen caps the state-table name length as defense-in-depth
// against pathological inputs. 64 is the cross-database safe minimum
// (PostgreSQL: 63 bytes, MySQL: 64 chars).
const tableNameMaxLen = 64

// Migrater applies SQL migration files to a database in numeric order of
// their leading prefix, recording each applied migration in a state table to
// guarantee idempotency.
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

// Run applies all pending migration files in numeric order.
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
	if len(m.table) > tableNameMaxLen {
		return fmt.Errorf("migrater: table name length %d exceeds maximum %d", len(m.table), tableNameMaxLen)
	}
	if !tableNameRe.MatchString(m.table) {
		return fmt.Errorf("migrater: table name %q is not a valid SQL identifier (must match %s)", m.table, tableNameRe.String())
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
// by the numeric value of their leading prefix, with the filename as a
// tiebreaker. Returns an error if the directory cannot be read or if two
// files share the same non-empty numeric ID.
//
// Files without a numeric prefix (e.g. "readme.sql") are returned in the
// sorted slice but are not deduplicated against each other; they are skipped
// individually by [Migrater.applyFile] without ever reaching the database.
func (m *Migrater) collectFiles() ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(m.fsys, m.dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %q: %w", m.dir, err)
	}

	type sortable struct {
		entry fs.DirEntry
		num   int
	}

	var files []sortable
	seen := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		id := migrationID(e.Name())
		if id != "" {
			if first, dup := seen[id]; dup {
				return nil, fmt.Errorf("duplicate migration ID %q: %s, %s", id, first, e.Name())
			}
			seen[id] = e.Name()
		}
		files = append(files, sortable{entry: e, num: m.numericKey(id)})
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].num != files[j].num {
			return files[i].num < files[j].num
		}
		return files[i].entry.Name() < files[j].entry.Name()
	})

	out := make([]fs.DirEntry, len(files))
	for i, f := range files {
		out[i] = f.entry
	}
	return out, nil
}

// numericKey returns the integer value of a migration ID for sort ordering.
// An empty id (file without a numeric prefix) maps to 0 so such files sort
// before any positive-prefixed migration; they are skipped at apply time.
//
// migrationID returns only digits for non-empty ids, so [strconv.Atoi] fails
// only on overflow (>~19 digits). The error branch maps to math.MaxInt as
// belt-and-braces — strconv.Atoi already clamps to math.MaxInt on positive
// overflow, but the explicit assignment makes the intent obvious without
// relying on package-level behavior.
func (*Migrater) numericKey(id string) int {
	if id == "" {
		return 0
	}
	n, err := strconv.Atoi(id)
	if err != nil {
		return math.MaxInt
	}
	return n
}

// applyFile processes a single migration entry: skips it if it has no numeric
// prefix, drift-checks it against the recorded content hash if it has already
// been applied, otherwise reads and applies it.
func (m *Migrater) applyFile(ctx context.Context, f fs.DirEntry, applied map[string]string) error {
	id := migrationID(f.Name())
	if id == "" {
		m.logf(m.debug, "skip %s: no numeric prefix", f.Name())
		return nil
	}

	contentb, err := fs.ReadFile(m.fsys, path.Join(m.dir, f.Name()))
	if err != nil {
		return fmt.Errorf("migrater: read %s: %w", f.Name(), err)
	}
	contentb = bytes.ReplaceAll(contentb, []byte("\r\n"), []byte("\n")) // normalize line endings to avoid spurious drift on Windows vs Unix hosts; this does not affect embedded migrations because Go embeds raw file bytes without conversion
	content := string(contentb)
	sum := sha256.Sum256(contentb)
	contentHash := hex.EncodeToString(sum[:])

	if storedHash, ok := applied[id]; ok {
		if storedHash != "" && storedHash != contentHash {
			m.logf(m.info,
				"drift detected: migration %s (%s) content has changed since it was applied "+
					"(stored hash %s, current %s) — file and database have diverged",
				id, f.Name(), storedHash, contentHash,
			)
		} else {
			m.logf(m.debug, "skip migration %s (already applied)", id)
		}
		return nil
	}

	m.logf(m.info, "applying migration %s (%s)", id, f.Name())
	m.logf(m.debug, "%s", content)
	start := time.Now()
	if err := m.apply(ctx, id, content, contentHash); err != nil {
		return fmt.Errorf("migrater: apply %s: %w", id, err)
	}
	m.logf(m.info, "migration %s applied in %s", id, time.Since(start).Round(time.Millisecond))

	return nil
}

// ensureTable creates the migration state table if it does not already exist
// and backfills the content_hash column for state tables created by versions
// of this package that did not include it.
func (m *Migrater) ensureTable(ctx context.Context) error {
	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (`+
			`id TEXT PRIMARY KEY, `+
			`content_hash TEXT, `+
			`applied_at %s NOT NULL DEFAULT CURRENT_TIMESTAMP`+
			`)`,
		m.table, m.timestampType(),
	)
	m.logf(m.debug, "ensure table: %s", query)
	if _, err := m.db.ExecContext(ctx, query); err != nil {
		return err
	}
	return m.ensureContentHashColumn(ctx)
}

// timestampType returns the SQL timestamp type appropriate for the configured
// database. PostgreSQL uses TIMESTAMPTZ so applied_at carries timezone
// information; SQLite stores TIMESTAMP as TEXT and is timezone-loose.
func (m *Migrater) timestampType() string {
	if m.postgres {
		return "TIMESTAMPTZ"
	}
	return "TIMESTAMP"
}

// ensureContentHashColumn adds the content_hash column to a state table that
// was created by an older version of this package. It is a no-op when the
// column already exists. Detection is portable: a SELECT on the column
// succeeds if it exists and errors otherwise; the table itself was just
// ensured by [Migrater.ensureTable], so any error indicates a missing column.
//
// Race handling: a concurrent Run on the same database may add the column
// between our probe and our ALTER. If the ALTER fails, we re-probe; a
// successful re-probe means the other caller won the race and we can proceed
// without surfacing an error.
func (m *Migrater) ensureContentHashColumn(ctx context.Context) error {
	if m.contentHashColumnExists(ctx) {
		return nil
	}
	alter := fmt.Sprintf("ALTER TABLE %s ADD COLUMN content_hash TEXT", m.table)
	m.logf(m.debug, "add content_hash column: %s", alter)
	if _, err := m.db.ExecContext(ctx, alter); err != nil {
		if m.contentHashColumnExists(ctx) {
			return nil
		}
		return err
	}
	return nil
}

// contentHashColumnExists reports whether the content_hash column exists in
// the state table. It probes via SELECT, which succeeds across SQLite,
// PostgreSQL, and MySQL when the column is present and errors otherwise.
func (m *Migrater) contentHashColumnExists(ctx context.Context) bool {
	probe := fmt.Sprintf("SELECT content_hash FROM %s LIMIT 0", m.table) //nolint:gosec // m.table is validated against tableNameRe in Run before this call
	rows, err := m.db.QueryContext(ctx, probe)
	if err != nil {
		return false
	}
	rows.Close() //nolint:sqlclosecheck // direct close: gocritic flags defer-just-before-return; probe yields zero rows by construction
	return true
}

// loadApplied performs a single SELECT to retrieve all already-applied
// migration IDs and their recorded content hashes. The returned map is keyed
// by migration ID; the value is the stored SHA-256 hex hash, or "" for rows
// recorded by versions of this package that did not store hashes.
func (m *Migrater) loadApplied(ctx context.Context) (map[string]string, error) {
	query := fmt.Sprintf("SELECT id, COALESCE(content_hash, '') FROM %s", m.table) //nolint:gosec // m.table is validated against tableNameRe in Run before this call
	m.logf(m.debug, "load applied: %s", query)

	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]string)
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, err
		}
		applied[id] = hash
	}
	return applied, rows.Err()
}

// apply executes a single migration inside a transaction, then records the
// migration ID and content hash in the state table. The transaction is rolled
// back on any error.
func (m *Migrater) apply(ctx context.Context, id, content, contentHash string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, content); err != nil {
		_ = tx.Rollback() //nolint:errcheck // best-effort; primary error is already returned
		return err
	}

	insertQuery := fmt.Sprintf( //nolint:gosec // m.table is validated against tableNameRe in Run before this call
		"INSERT INTO %s (id, content_hash) VALUES (%s, %s)",
		m.table, m.placeholder(1), m.placeholder(2),
	)
	m.logf(m.debug, "record state: %s [%s, %s]", insertQuery, id, contentHash)
	if _, err := tx.ExecContext(ctx, insertQuery, id, contentHash); err != nil {
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
