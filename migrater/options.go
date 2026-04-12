package migrater

import (
	"fmt"
	"io/fs"
	"os"
)

// LogFunc is the function signature used for all log levels in Migrater.
// It follows the fmt.Printf convention: a format string followed by variadic arguments.
// A nil LogFunc is a no-op; use this to silence a specific level.
type LogFunc func(format string, v ...any)

// Default log functions used by New when no explicit log options are provided.
// Replace any of these package-level variables to change the default behavior for
// all subsequent New calls in the same process.
var (
	// DefaultInfoLog logs informational messages to stdout with an [INFO] prefix.
	DefaultInfoLog LogFunc = func(format string, v ...any) {
		fmt.Printf("[INFO] migrater: "+format+"\n", v...)
	}

	// DefaultDebugLog is nil by default (silent). Assign a function to enable debug output.
	DefaultDebugLog LogFunc

	// DefaultRecoverLog logs recovered panics to stdout with a [RECOVER] prefix.
	// It is called inside the defer/recover block in Run, making the library panic-safe.
	DefaultRecoverLog LogFunc = func(format string, v ...any) {
		fmt.Printf("[RECOVER] migrater: "+format+"\n", v...)
	}
)

// DefaultTableName is the name of the table used to track applied migrations.
const DefaultTableName = "migrater_state"

// DefaultDir is the default migrations directory, relative to the working directory.
const DefaultDir = "migrations"

// Option is a function that configures a [Migrater] instance.
// Options are applied in order during [New].
type Option func(*Migrater)

// WithDir sets the migrations directory on the OS filesystem.
// It is a convenience wrapper around [WithFS] using [os.DirFS].
//
// Example:
//
//	m := migrater.New(db, migrater.WithDir("db/migrations"))
func WithDir(dir string) Option {
	return func(m *Migrater) {
		m.fsys = os.DirFS(dir)
		m.dir = "."
	}
}

// WithFS sets the [fs.FS] and subdirectory used to read migration files.
// This is the primary option for embedded migrations:
//
//	//go:embed migrations
//	var migrationsFS embed.FS
//
//	m := migrater.New(db, migrater.WithFS(migrationsFS, "migrations"))
func WithFS(fsys fs.FS, dir string) Option {
	return func(m *Migrater) {
		m.fsys = fsys
		m.dir = dir
	}
}

// WithInfoLog sets the function used for informational log messages, such as
// "applying migration 001" or "skipping migration 002 (already applied)".
// Pass nil to silence info logging.
func WithInfoLog(fn LogFunc) Option {
	return func(m *Migrater) {
		m.info = fn
	}
}

// WithDebugLog sets the function used for debug-level log messages.
// Debug messages include query strings and row-level detail.
// Pass nil to silence debug logging (the default).
func WithDebugLog(fn LogFunc) Option {
	return func(m *Migrater) {
		m.debug = fn
	}
}

// WithRecoverLog sets the function called when a panic is recovered inside [Migrater.Run].
// The recovered value is formatted and passed as the first argument.
// Pass nil to silence panic-recovery logging (the panic is still converted to an error).
func WithRecoverLog(fn LogFunc) Option {
	return func(m *Migrater) {
		m.recoverLog = fn
	}
}

// WithTableName overrides the default state table name ("migrater_state").
// Use this when the default name conflicts with an existing table in your schema.
func WithTableName(name string) Option {
	return func(m *Migrater) {
		m.table = name
	}
}

// WithPostgres switches the SQL placeholder style from ? (SQLite) to $N (PostgreSQL).
// Apply this option when connecting to a PostgreSQL database.
func WithPostgres() Option {
	return func(m *Migrater) {
		m.postgres = true
	}
}
