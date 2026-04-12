package migrater

import (
	"context"
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

// stateSelects returns all SELECT … migrater_state executions.
func stateSelects(conn *mockConn) []execRecord {
	return conn.RecordsContaining("SELECT id FROM migrater_state")
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

// TestRun_SortsFiles verifies that files are applied in lexicographic (numeric) order.
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

// sentinel errors used in tests.
var (
	errTest = fmt.Errorf("test migration error")
)
