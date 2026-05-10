package migrater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

// mapFS builds a fstest.MapFS from alternating name/content pairs.
func mapFS(t *testing.T, pairs ...string) fstest.MapFS {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("mapFS: pairs must be even (name, content, ...)")
	}
	m := fstest.MapFS{}
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = &fstest.MapFile{Data: []byte(pairs[i+1])}
	}
	return m
}

// newMigrater creates a Migrater backed by the mock conn with the given fs and dir.
func newMigrater(t *testing.T, conn *mockConn, fsys fstest.MapFS, dir string) *Migrater {
	t.Helper()
	db, err := newTestDB(conn)
	if err != nil {
		t.Fatalf("newTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db,
		WithFS(fsys, dir),
		WithInfoLog(nil),
		WithDebugLog(nil),
		WithRecoverLog(nil),
	)
}

// stateInserts returns all INSERT INTO migrater_state executions.
func stateInserts(conn *mockConn) []execRecord {
	return conn.RecordsContaining("INSERT INTO migrater_state")
}

// stateSelects returns all loadApplied SELECT executions on the state table.
// It does not match the probe SELECT issued to detect content_hash existence.
func stateSelects(conn *mockConn) []execRecord {
	return conn.RecordsContaining("SELECT id, COALESCE")
}

// argString converts the first driver.Value arg to a string for assertions.
func argString(r execRecord) string {
	if len(r.args) == 0 {
		return ""
	}
	if s, ok := r.args[0].(string); ok {
		return s
	}
	return fmt.Sprintf("%v", r.args[0])
}

// TestRun_AppliesAll verifies that two new migration files are both applied in order.
func TestRun_AppliesAll(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
		"migrations/002.sql", "CREATE TABLE b (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 2 {
		t.Fatalf("expected 2 state inserts, got %d: %v", len(inserts), inserts)
	}
	if argString(inserts[0]) != "001" {
		t.Errorf("first insert arg = %q, want %q", argString(inserts[0]), "001")
	}
	if argString(inserts[1]) != "002" {
		t.Errorf("second insert arg = %q, want %q", argString(inserts[1]), "002")
	}
}

// TestRun_SkipsApplied verifies that an already-applied migration is skipped.
func TestRun_SkipsApplied(t *testing.T) {
	conn := newMockConn([]string{"001"}, nil)
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
		"migrations/002.sql", "CREATE TABLE b (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 1 {
		t.Fatalf("expected 1 state insert (only 002), got %d: %v", len(inserts), inserts)
	}
	if argString(inserts[0]) != "002" {
		t.Errorf("insert arg = %q, want %q", argString(inserts[0]), "002")
	}
}

// TestRun_SingleSelect verifies that only one SELECT on the state table is issued
// regardless of how many migration files exist.
func TestRun_SingleSelect(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE x (id INTEGER);",
		"migrations/002.sql", "CREATE TABLE y (id INTEGER);",
		"migrations/003.sql", "CREATE TABLE z (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	selects := stateSelects(conn)
	if len(selects) != 1 {
		t.Errorf("expected exactly 1 SELECT on state table, got %d", len(selects))
	}
}

// TestRun_SortsFiles verifies that zero-padded files are applied in numeric order
// regardless of fs iteration order.
func TestRun_SortsFiles(t *testing.T) {
	conn := newMockConn(nil, nil)
	// fstest.MapFS does not guarantee iteration order; rely on migrater's sort.
	fsys := mapFS(t,
		"migrations/003.sql", "CREATE TABLE c (id INTEGER);",
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
		"migrations/002.sql", "CREATE TABLE b (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 3 {
		t.Fatalf("expected 3 state inserts, got %d", len(inserts))
	}
	expected := []string{"001", "002", "003"}
	for i, exp := range expected {
		got := argString(inserts[i])
		if got != exp {
			t.Errorf("insert #%d: got %q, want %q", i, got, exp)
		}
	}
}

// TestRun_EmptyDir verifies that an empty migrations directory is a no-op.
func TestRun_EmptyDir(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := fstest.MapFS{
		"migrations/.keep": &fstest.MapFile{Data: []byte{}},
	}
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run on empty dir: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 0 {
		t.Errorf("expected no state inserts for empty dir, got %d", len(inserts))
	}
}

// TestRun_MissingDir verifies that a non-existent directory returns an error.
func TestRun_MissingDir(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := fstest.MapFS{}
	m := newMigrater(t, conn, fsys, "no-such-dir")

	if err := m.Run(context.Background()); err == nil {
		t.Fatal("expected error for missing dir, got nil")
	}
}

// TestRun_MigrationError verifies that a failing migration stops execution.
func TestRun_MigrationError(t *testing.T) {
	conn := newMockConn(nil, map[string]error{
		"CREATE TABLE a": errTest,
	})
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
		"migrations/002.sql", "CREATE TABLE b (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}

	inserts := stateInserts(conn)
	if len(inserts) != 0 {
		t.Errorf("expected no successful state inserts after failure, got %d", len(inserts))
	}
}

// TestRun_RecoversPanic verifies that a panic inside Run is caught and returned as an error.
// The mock is configured to panic when the migration SQL is executed, so the panic
// originates from inside m.Run() itself and exercises the real defer/recover path.
func TestRun_RecoversPanic(t *testing.T) {
	conn := newMockConn(nil, nil)
	conn.panicOn = "CREATE TABLE a" // panic when executing the migration content
	fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")

	recoverCalled := false
	db, err := newTestDB(conn)
	if err != nil {
		t.Fatalf("newTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	m := New(db,
		WithFS(fsys, "migrations"),
		WithInfoLog(nil),
		WithDebugLog(nil),
		WithRecoverLog(func(_ string, _ ...any) { recoverCalled = true }),
	)

	runErr := m.Run(context.Background())
	if runErr == nil {
		t.Fatal("expected error from recovered panic, got nil")
	}
	if !strings.Contains(runErr.Error(), "panic") {
		t.Errorf("error message should mention panic, got: %v", runErr)
	}
	if !recoverCalled {
		t.Error("expected recover log to be called")
	}
}

// TestRun_Postgres verifies that WithPostgres() causes $1 placeholders in state INSERT.
func TestRun_Postgres(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
	)
	db, err := newTestDB(conn)
	if err != nil {
		t.Fatalf("newTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	m := New(db,
		WithFS(fsys, "migrations"),
		WithPostgres(),
		WithInfoLog(nil),
		WithDebugLog(nil),
		WithRecoverLog(nil),
	)

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) == 0 {
		t.Fatal("expected state insert, got none")
	}
	if !strings.Contains(inserts[0].query, "$1") {
		t.Errorf("expected $1 placeholder in %q", inserts[0].query)
	}
}

// TestRun_SkipsNoPrefix verifies that a .sql file with no numeric prefix is silently skipped.
func TestRun_SkipsNoPrefix(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
		"migrations/readme.sql", "-- this has no numeric prefix",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 1 {
		t.Fatalf("expected 1 state insert (only 001), got %d", len(inserts))
	}
	if argString(inserts[0]) != "001" {
		t.Errorf("insert arg = %q, want %q", argString(inserts[0]), "001")
	}
}

// TestRun_EnsureTableError verifies that a failure creating the state table is returned.
func TestRun_EnsureTableError(t *testing.T) {
	conn := newMockConn(nil, map[string]error{
		"CREATE TABLE": errTest,
	})
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	err := m.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from ensureTable failure, got nil")
	}
}

// TestMigrationID verifies the numeric prefix extraction function.
func TestMigrationID(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"001.sql", "001"},
		{"002-create-users.sql", "002"},
		{"003-some-long-description.sql", "003"},
		{"42.sql", "42"},
		{"readme.sql", ""},
		{"abc-001.sql", ""},
		{".sql", ""},
	}

	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			got := migrationID(tc.filename)
			if got != tc.want {
				t.Errorf("migrationID(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}

// TestRun_NumericSort verifies that files with unpadded numeric prefixes are
// applied in numeric order (1, 2, 10, 11) rather than lexicographic order
// (1, 10, 11, 2).
func TestRun_NumericSort(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/2.sql", "CREATE TABLE b (id INTEGER);",
		"migrations/11.sql", "CREATE TABLE k (id INTEGER);",
		"migrations/1.sql", "CREATE TABLE a (id INTEGER);",
		"migrations/10.sql", "CREATE TABLE j (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 4 {
		t.Fatalf("expected 4 state inserts, got %d", len(inserts))
	}
	expected := []string{"1", "2", "10", "11"}
	for i, exp := range expected {
		got := argString(inserts[i])
		if got != exp {
			t.Errorf("insert #%d: got %q, want %q", i, got, exp)
		}
	}
}

// TestRun_NumericSortMixedPadding verifies that zero-padded and unpadded files
// are interleaved in correct numeric order.
//
// Note: padding consistency is a project hygiene concern. The state-table ID is
// the raw leading-digits string, so 001.sql and 1.sql are stored as distinct
// IDs ("001" vs "1") and a project that mixes both styles across deploys will
// re-apply migration 1. Pick one style per project.
func TestRun_NumericSortMixedPadding(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/010.sql", "CREATE TABLE j (id INTEGER);",
		"migrations/2.sql", "CREATE TABLE b (id INTEGER);",
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 3 {
		t.Fatalf("expected 3 state inserts, got %d", len(inserts))
	}
	expected := []string{"001", "2", "010"}
	for i, exp := range expected {
		got := argString(inserts[i])
		if got != exp {
			t.Errorf("insert #%d: got %q, want %q", i, got, exp)
		}
	}
}

// TestRun_DuplicateID verifies that two files sharing the same numeric prefix
// cause Run to error before any migration is applied, naming both files in
// the error message.
func TestRun_DuplicateID(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/001.sql", "CREATE TABLE a (id INTEGER);",
		"migrations/001-create-users.sql", "CREATE TABLE u (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	err := m.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for duplicate migration ID, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate migration ID") {
		t.Errorf("error should mention duplicate migration ID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "001.sql") || !strings.Contains(err.Error(), "001-create-users.sql") {
		t.Errorf("error should name both colliding files, got: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 0 {
		t.Errorf("expected no state inserts when duplicate detected, got %d", len(inserts))
	}
}

// TestRun_TableNameInvalid verifies that a table name with characters outside
// the SQL-identifier set, or exceeding the length cap, causes Run to fail
// before any DB call.
func TestRun_TableNameInvalid(t *testing.T) {
	cases := []string{
		"users; DROP TABLE x",
		"foo bar",
		"foo-bar",
		"1leading_digit",
		"foo'bar",
		`"quoted"`,
		"public.migrations",       // schema-qualified
		"tàble",                   // non-ASCII letter (Latin-1 supplement)
		"таблица",                 // non-ASCII letter (Cyrillic)
		strings.Repeat("a", 65),   // exceeds tableNameMaxLen
		strings.Repeat("x", 1000), // pathological length (DOS guard)
	}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			conn := newMockConn(nil, nil)
			fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")
			db, err := newTestDB(conn)
			if err != nil {
				t.Fatalf("newTestDB: %v", err)
			}
			t.Cleanup(func() { db.Close() })

			m := New(db,
				WithFS(fsys, "migrations"),
				WithTableName(name),
				WithInfoLog(nil),
				WithDebugLog(nil),
				WithRecoverLog(nil),
			)

			runErr := m.Run(context.Background())
			if runErr == nil {
				t.Fatal("expected error for invalid table name, got nil")
			}
			if !strings.Contains(runErr.Error(), "table name") {
				t.Errorf("error should mention table name, got: %v", runErr)
			}
			if recs := conn.Records(); len(recs) != 0 {
				t.Errorf("expected no DB calls for invalid table name, got %d: %v", len(recs), recs)
			}
		})
	}
}

// TestRun_TableNameValid verifies that valid SQL-identifier table names pass
// the regex check and reach the DB layer.
func TestRun_TableNameValid(t *testing.T) {
	cases := []string{
		"migrater_state",
		"_state",
		"State1",
		"a",
		"_", // solo underscore — valid SQL identifier
		"my_app_migrations",
		strings.Repeat("a", 64), // exactly tableNameMaxLen
	}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			conn := newMockConn(nil, nil)
			fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")
			db, err := newTestDB(conn)
			if err != nil {
				t.Fatalf("newTestDB: %v", err)
			}
			t.Cleanup(func() { db.Close() })

			m := New(db,
				WithFS(fsys, "migrations"),
				WithTableName(name),
				WithInfoLog(nil),
				WithDebugLog(nil),
				WithRecoverLog(nil),
			)

			if err := m.Run(context.Background()); err != nil {
				t.Fatalf("Run with valid table name %q: %v", name, err)
			}
		})
	}
}

// TestRun_RunTwice verifies that calling Run after the same migrations have
// already been applied is a no-op. Continuity between runs is simulated with
// a fresh mock conn pre-loaded with the recorded IDs and hashes — equivalent
// to what a persistent database would carry forward.
func TestRun_RunTwice(t *testing.T) {
	contentA := "CREATE TABLE a (id INTEGER);"
	contentB := "CREATE TABLE b (id INTEGER);"
	fsys := mapFS(t,
		"migrations/001.sql", contentA,
		"migrations/002.sql", contentB,
	)

	// First Run with empty state — applies both migrations.
	conn1 := newMockConn(nil, nil)
	m1 := newMigrater(t, conn1, fsys, "migrations")
	if err := m1.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if got := len(stateInserts(conn1)); got != 2 {
		t.Fatalf("first Run: expected 2 inserts, got %d", got)
	}

	// Second Run starting from the state the first Run produced — must be
	// a complete no-op (no new INSERTs, no errors).
	sumA := sha256.Sum256([]byte(contentA))
	sumB := sha256.Sum256([]byte(contentB))
	conn2 := newMockConnWithHashes(map[string]string{
		"001": hex.EncodeToString(sumA[:]),
		"002": hex.EncodeToString(sumB[:]),
	}, nil)
	m2 := newMigrater(t, conn2, fsys, "migrations")
	if err := m2.Run(context.Background()); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := len(stateInserts(conn2)); got != 0 {
		t.Errorf("second Run: expected 0 inserts (idempotent), got %d", got)
	}
}

// TestRun_CtxCanceled verifies that a canceled context fails Run before any
// state insert and that no transaction is left open. The returned error must
// wrap context.Canceled so callers can distinguish cancellation from real
// migration failures.
func TestRun_CtxCanceled(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")
	m := newMigrater(t, conn, fsys, "migrations")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := m.Run(ctx)
	if err == nil {
		t.Fatal("expected error from canceled ctx, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap context.Canceled, got: %v", err)
	}
	if inserts := stateInserts(conn); len(inserts) != 0 {
		t.Errorf("expected no state inserts after cancel, got %d", len(inserts))
	}
}

// TestRun_EmptyMigrationFile verifies that an empty .sql file is recorded as
// applied and does not block subsequent migrations.
func TestRun_EmptyMigrationFile(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t,
		"migrations/001.sql", "",
		"migrations/002.sql", "CREATE TABLE b (id INTEGER);",
	)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	inserts := stateInserts(conn)
	if len(inserts) != 2 {
		t.Fatalf("expected 2 inserts (empty file still recorded), got %d", len(inserts))
	}
}

// TestRun_ContentHashRecorded verifies that the INSERT into the state table
// includes the SHA-256 hex digest of the migration's content as the second
// argument.
func TestRun_ContentHashRecorded(t *testing.T) {
	content := "CREATE TABLE a (id INTEGER);"
	sum := sha256.Sum256([]byte(content))
	expected := hex.EncodeToString(sum[:])

	conn := newMockConn(nil, nil)
	fsys := mapFS(t, "migrations/001.sql", content)
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	inserts := stateInserts(conn)
	if len(inserts) != 1 {
		t.Fatalf("expected 1 insert, got %d", len(inserts))
	}
	if len(inserts[0].args) < 2 {
		t.Fatalf("expected 2 args (id, hash) in INSERT, got %d", len(inserts[0].args))
	}
	got, ok := inserts[0].args[1].(string)
	if !ok {
		t.Fatalf("hash arg is not a string: %T (%v)", inserts[0].args[1], inserts[0].args[1])
	}
	if got != expected {
		t.Errorf("hash arg = %q, want %q", got, expected)
	}
}

// TestRun_DriftDetected verifies that modifying an already-applied migration's
// file content is surfaced as an info-level log message on the next Run,
// without aborting Run or producing further state inserts.
func TestRun_DriftDetected(t *testing.T) {
	originalContent := "CREATE TABLE a (id INTEGER);"
	modifiedContent := "CREATE TABLE a (id INTEGER, name TEXT);"
	originalSum := sha256.Sum256([]byte(originalContent))

	conn := newMockConnWithHashes(map[string]string{
		"001": hex.EncodeToString(originalSum[:]),
	}, nil)
	fsys := mapFS(t, "migrations/001.sql", modifiedContent)
	db, err := newTestDB(conn)
	if err != nil {
		t.Fatalf("newTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	var infoMessages []string
	m := New(db,
		WithFS(fsys, "migrations"),
		WithInfoLog(func(format string, v ...any) {
			infoMessages = append(infoMessages, fmt.Sprintf(format, v...))
		}),
		WithDebugLog(nil),
		WithRecoverLog(nil),
	)

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run should not error on drift: %v", err)
	}

	var driftMsg string
	for _, msg := range infoMessages {
		if strings.Contains(msg, "drift detected") {
			driftMsg = msg
			break
		}
	}
	if driftMsg == "" {
		t.Fatalf("expected info log mentioning drift, got messages: %v", infoMessages)
	}
	if !strings.Contains(driftMsg, "001.sql") {
		t.Errorf("drift message should name the file, got: %s", driftMsg)
	}
	if inserts := stateInserts(conn); len(inserts) != 0 {
		t.Errorf("expected no state inserts (migration is already applied), got %d", len(inserts))
	}
}

// TestRun_LegacyHashSkipsDrift verifies that a legacy applied row (NULL hash)
// does not trigger drift detection even if the file has been modified — there
// is no recorded hash to compare against.
func TestRun_LegacyHashSkipsDrift(t *testing.T) {
	conn := newMockConn([]string{"001"}, nil) // legacy: empty hash
	fsys := mapFS(t, "migrations/001.sql", "completely different content from whatever was applied")
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run with legacy applied row should not error: %v", err)
	}
	if inserts := stateInserts(conn); len(inserts) != 0 {
		t.Errorf("expected no state inserts (already applied), got %d", len(inserts))
	}
}

// TestRun_TimestampType_Postgres verifies that WithPostgres() causes
// CREATE TABLE to use TIMESTAMPTZ for applied_at.
func TestRun_TimestampType_Postgres(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")
	db, err := newTestDB(conn)
	if err != nil {
		t.Fatalf("newTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	m := New(db,
		WithFS(fsys, "migrations"),
		WithPostgres(),
		WithInfoLog(nil),
		WithDebugLog(nil),
		WithRecoverLog(nil),
	)
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	creates := conn.RecordsContaining("CREATE TABLE IF NOT EXISTS")
	if len(creates) == 0 {
		t.Fatal("expected CREATE TABLE statement, got none")
	}
	if !strings.Contains(creates[0].query, "TIMESTAMPTZ") {
		t.Errorf("expected TIMESTAMPTZ in CREATE TABLE on Postgres, got: %s", creates[0].query)
	}
}

// TestRun_TimestampType_SQLite verifies that the default (non-Postgres) mode
// uses plain TIMESTAMP, not TIMESTAMPTZ, in CREATE TABLE.
func TestRun_TimestampType_SQLite(t *testing.T) {
	conn := newMockConn(nil, nil)
	fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	creates := conn.RecordsContaining("CREATE TABLE IF NOT EXISTS")
	if len(creates) == 0 {
		t.Fatal("expected CREATE TABLE statement, got none")
	}
	if strings.Contains(creates[0].query, "TIMESTAMPTZ") {
		t.Errorf("expected plain TIMESTAMP on SQLite, got TIMESTAMPTZ: %s", creates[0].query)
	}
	if !strings.Contains(creates[0].query, "TIMESTAMP") {
		t.Errorf("expected TIMESTAMP in CREATE TABLE, got: %s", creates[0].query)
	}
}

// TestRun_AddsContentHashColumn verifies that when the content_hash probe
// errors (simulating a legacy state table without the column), an
// ALTER TABLE ADD COLUMN is issued before the rest of Run proceeds.
func TestRun_AddsContentHashColumn(t *testing.T) {
	// errOn matches the probe (the only SELECT containing "LIMIT 0") but not
	// the loadApplied SELECT (which has no LIMIT clause).
	conn := newMockConn(nil, map[string]error{
		"LIMIT 0": errTest,
	})
	fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	alters := conn.RecordsContaining("ADD COLUMN content_hash")
	if len(alters) != 1 {
		t.Fatalf("expected 1 ALTER TABLE ADD COLUMN, got %d", len(alters))
	}
	// The migration must still apply after the column is added.
	inserts := stateInserts(conn)
	if len(inserts) != 1 {
		t.Errorf("expected migration to apply after column backfill, got %d inserts", len(inserts))
	}
}

// TestRun_AddsContentHashColumn_AlterRace verifies the race-recovery path
// in ensureContentHashColumn: a concurrent caller can add the column between
// our probe and our ALTER, in which case the ALTER fails. We re-probe; if the
// column now exists, the error is swallowed and Run continues normally.
//
// Sequence simulated by errOnCount:
//  1. probe        → matches "LIMIT 0" (count 1→0), returns errTest
//  2. ALTER        → matches "ADD COLUMN" (count 1→0), returns errTest
//  3. re-probe     → no match remaining, returns nil → column "exists"
//  4. Run continues, applies the migration
func TestRun_AddsContentHashColumn_AlterRace(t *testing.T) {
	conn := newMockConn(nil, map[string]error{
		"LIMIT 0":    errTest,
		"ADD COLUMN": errTest,
	})
	conn.errOnCount = map[string]int{
		"LIMIT 0":    1, // probe fails first time, succeeds on re-probe
		"ADD COLUMN": 1, // ALTER fails (column already added by other caller)
	}
	fsys := mapFS(t, "migrations/001.sql", "CREATE TABLE a (id INTEGER);")
	m := newMigrater(t, conn, fsys, "migrations")

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run should not error after race recovery: %v", err)
	}

	alters := conn.RecordsContaining("ADD COLUMN content_hash")
	if len(alters) != 1 {
		t.Errorf("expected 1 ALTER attempt, got %d", len(alters))
	}
	probes := conn.RecordsContaining("SELECT content_hash FROM")
	if len(probes) != 2 {
		t.Errorf("expected 2 probes (initial + re-probe after ALTER fail), got %d", len(probes))
	}
	inserts := stateInserts(conn)
	if len(inserts) != 1 {
		t.Errorf("expected migration to apply after race recovery, got %d inserts", len(inserts))
	}
}

// sentinel errors used in tests.
var (
	errTest = fmt.Errorf("test migration error")
)
