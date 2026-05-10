package migrater

// mockDriver, mockConn, mockStmt, and mockRows implement the database/sql/driver
// interfaces using only the standard library. They are used exclusively in tests.

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// execRecord captures a single statement execution with its bound arguments.
type execRecord struct {
	query string
	args  []driver.Value
}

// appliedRow represents a row in the simulated state table. An empty hash
// models a NULL content_hash, i.e. a row inserted by an older version of the
// migrater package that did not record content hashes.
type appliedRow struct {
	id   string
	hash string
}

// mockDriver is a minimal driver.Driver that creates mockConn instances.
type mockDriver struct {
	conn *mockConn
}

func (d *mockDriver) Open(_ string) (driver.Conn, error) {
	return d.conn, nil
}

// mockConn records every statement execution against it and returns controlled results.
type mockConn struct {
	mu         sync.Mutex
	records    []execRecord     // all executions, in order
	applied    []appliedRow     // rows returned by SELECT on the state table
	errOn      map[string]error // substring → error; matching queries return that error
	errOnCount map[string]int   // optional: limit errOn to the first N matches per substring; absent key → unlimited
	panicOn    string           // if non-empty, Exec/Query panics when query contains this substring
}

func (c *mockConn) record(q string, args []driver.Value) {
	c.mu.Lock()
	c.records = append(c.records, execRecord{query: q, args: args})
	c.mu.Unlock()
}

// Records returns a snapshot of all recorded executions.
func (c *mockConn) Records() []execRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]execRecord, len(c.records))
	copy(out, c.records)
	return out
}

// RecordsContaining returns executions whose query contains substr.
func (c *mockConn) RecordsContaining(substr string) []execRecord {
	var out []execRecord
	for _, r := range c.Records() {
		if strings.Contains(r.query, substr) {
			out = append(out, r)
		}
	}
	return out
}

func (c *mockConn) errorFor(query string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for substr, err := range c.errOn {
		if !strings.Contains(query, substr) {
			continue
		}
		if c.errOnCount != nil {
			remaining, capped := c.errOnCount[substr]
			if capped {
				if remaining <= 0 {
					continue
				}
				c.errOnCount[substr] = remaining - 1
			}
		}
		return err
	}
	return nil
}

// Prepare always succeeds (no syntax-error simulation). errOn is consulted
// at execution time (Exec/Query), which matches real driver behavior:
// Prepare-time errors are typically syntax issues we don't model, while
// runtime constraint or DDL errors fire on actual execution.
func (c *mockConn) Prepare(query string) (driver.Stmt, error) {
	isSelect := strings.HasPrefix(strings.TrimSpace(strings.ToUpper(query)), "SELECT")
	return &mockStmt{conn: c, query: query, isSelect: isSelect}, nil
}

func (c *mockConn) Close() error              { return nil }
func (c *mockConn) Begin() (driver.Tx, error) { return &mockTx{}, nil }

// mockTx is a no-op transaction.
type mockTx struct{}

func (t *mockTx) Commit() error   { return nil }
func (t *mockTx) Rollback() error { return nil }

// mockStmt executes statements against the mockConn, recording each execution.
type mockStmt struct {
	conn     *mockConn
	query    string
	isSelect bool
}

func (s *mockStmt) Close() error { return nil }

func (s *mockStmt) NumInput() int { return -1 } // -1 means driver doesn't check

func (s *mockStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.conn.record(s.query, args)
	s.conn.mu.Lock()
	panicOn := s.conn.panicOn
	s.conn.mu.Unlock()
	if panicOn != "" && strings.Contains(s.query, panicOn) {
		panic("mock panic: " + s.query)
	}
	if err := s.conn.errorFor(s.query); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (s *mockStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.conn.record(s.query, args)
	s.conn.mu.Lock()
	panicOn := s.conn.panicOn
	s.conn.mu.Unlock()
	if panicOn != "" && strings.Contains(s.query, panicOn) {
		panic("mock panic: " + s.query)
	}
	if err := s.conn.errorFor(s.query); err != nil {
		return nil, err
	}
	if !s.isSelect {
		return &mockRows{}, nil
	}

	// SELECT shape inference: the package issues two SELECTs.
	//   - "SELECT content_hash FROM <table> LIMIT 0" probes for the column.
	//     Returns 0 rows (column exists). errOn can simulate "column missing"
	//     so the package falls through to ALTER TABLE.
	//   - "SELECT id, COALESCE(content_hash, '') FROM <table>" loads applied
	//     rows. Returns 2 columns per applied row.
	if strings.Contains(s.query, "LIMIT 0") {
		return &mockRows{columns: []string{"content_hash"}}, nil
	}
	s.conn.mu.Lock()
	rows := make([]appliedRow, len(s.conn.applied))
	copy(rows, s.conn.applied)
	s.conn.mu.Unlock()
	return &mockRows{rows: rows, columns: []string{"id", "content_hash"}}, nil
}

// mockRows yields one row per applied migration row.
type mockRows struct {
	rows    []appliedRow
	columns []string
	pos     int
}

func (r *mockRows) Columns() []string { return r.columns }
func (r *mockRows) Close() error      { return nil }

func (r *mockRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.pos]
	r.pos++
	for i, col := range r.columns {
		switch col {
		case "id":
			dest[i] = row.id
		case "content_hash":
			dest[i] = row.hash
		}
	}
	return nil
}

// driverSeq generates unique driver names so each test gets a fresh sql.DB.
var driverSeq atomic.Int64

// newMockConn creates a mockConn pre-loaded with the given applied migration
// IDs. Each ID becomes a row with an empty content_hash (legacy semantics).
func newMockConn(applied []string, errOn map[string]error) *mockConn {
	if errOn == nil {
		errOn = map[string]error{}
	}
	rows := make([]appliedRow, 0, len(applied))
	for _, id := range applied {
		rows = append(rows, appliedRow{id: id})
	}
	return &mockConn{
		applied: rows,
		errOn:   errOn,
	}
}

// newMockConnWithHashes creates a mockConn pre-loaded with applied migration
// rows including their stored content hashes. Use this for drift-detection
// tests where the hash value matters.
func newMockConnWithHashes(applied map[string]string, errOn map[string]error) *mockConn {
	if errOn == nil {
		errOn = map[string]error{}
	}
	rows := make([]appliedRow, 0, len(applied))
	for id, hash := range applied {
		rows = append(rows, appliedRow{id: id, hash: hash})
	}
	return &mockConn{
		applied: rows,
		errOn:   errOn,
	}
}

// newTestDB registers a fresh mockDriver and returns a *sql.DB backed by conn.
func newTestDB(conn *mockConn) (*sql.DB, error) {
	name := fmt.Sprintf("mock-%d", driverSeq.Add(1))
	sql.Register(name, &mockDriver{conn: conn})
	return sql.Open(name, "")
}
