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

// mockDriver is a minimal driver.Driver that creates mockConn instances.
type mockDriver struct {
	conn *mockConn
}

func (d *mockDriver) Open(_ string) (driver.Conn, error) {
	return d.conn, nil
}

// mockConn records every statement execution against it and returns controlled results.
type mockConn struct {
	mu      sync.Mutex
	records []execRecord     // all executions, in order
	applied []string         // IDs returned by SELECT on the state table
	errOn   map[string]error // substring → error; matching queries return that error
	panicOn string           // if non-empty, Exec/Query panics when query contains this substring
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
		if strings.Contains(query, substr) {
			return err
		}
	}
	return nil
}

func (c *mockConn) Prepare(query string) (driver.Stmt, error) {
	if err := c.errorFor(query); err != nil {
		return nil, err
	}
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
	if s.isSelect {
		s.conn.mu.Lock()
		ids := make([]string, len(s.conn.applied))
		copy(ids, s.conn.applied)
		s.conn.mu.Unlock()
		return &mockRows{ids: ids}, nil
	}
	return &mockRows{}, nil
}

// mockRows yields one row per applied migration ID.
type mockRows struct {
	ids []string
	pos int
}

func (r *mockRows) Columns() []string { return []string{"id"} }
func (r *mockRows) Close() error      { return nil }

func (r *mockRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.ids) {
		return io.EOF
	}
	dest[0] = r.ids[r.pos]
	r.pos++
	return nil
}

// driverSeq generates unique driver names so each test gets a fresh sql.DB.
var driverSeq atomic.Int64

// newMockConn creates a mockConn pre-loaded with the given applied migration IDs.
func newMockConn(applied []string, errOn map[string]error) *mockConn {
	if errOn == nil {
		errOn = map[string]error{}
	}
	return &mockConn{
		applied: applied,
		errOn:   errOn,
	}
}

// newTestDB registers a fresh mockDriver and returns a *sql.DB backed by conn.
func newTestDB(conn *mockConn) (*sql.DB, error) {
	name := fmt.Sprintf("mock-%d", driverSeq.Add(1))
	sql.Register(name, &mockDriver{conn: conn})
	return sql.Open(name, "")
}
