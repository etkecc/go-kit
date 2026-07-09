# migrater

[![Go Reference](https://pkg.go.dev/badge/github.com/etkecc/go-kit.svg)](https://pkg.go.dev/github.com/etkecc/go-kit/migrater)

Numbered `.sql` files go in, your database catches up. Each file runs once, inside its own transaction, and the ID gets recorded so the next run is a no-op. Point it at a directory or an embedded `fs.FS`, call `Run`, done. Deliberately small: no DSL, no rollback, no magic.

Lives in the root module, stdlib only.

```go
go get github.com/etkecc/go-kit
```

```go
import "github.com/etkecc/go-kit/migrater"

//go:embed migrations/*.sql
var migrations embed.FS

m := migrater.New(db, migrater.WithFS(migrations, "migrations"))
if err := m.Run(ctx); err != nil {
    log.Fatal(err)
}
```

## Naming: a number, then whatever you want

Files need a `.sql` extension and a leading numeric prefix. The number sets the order, so zero-pad it for a directory listing that sorts the way your eyes do:

```
001.sql
002-create-users.sql
003-add-index.sql
```

The migration ID stored in the state table is the number only (`001`, `003`); the description after it is for humans. Two files with the **same** number is an error before anything runs, so you can't accidentally have two `002`s racing to define reality.

## The three choices that will surprise you

- **Forward-only. No down migrations.** There is no rollback and there never will be. If you need to undo something, that's a new numbered migration that undoes it. Rollbacks lie about what actually ran in prod; a forward fix doesn't.
- **Drift is logged, not fatal.** On apply, migrater stores a SHA-256 of each file's content. On later runs it re-hashes applied files, and if one changed since it ran (the file and the database have diverged), it **logs** that and keeps going. It does not error and it does not re-apply. This is on purpose: it's telling you something's off, not refusing to boot over it. Rows from before content-hashing carry a NULL hash and are skipped, no false alarms.
- **One `ExecContext` per file.** Whether a single file may hold multiple statements is the driver's call, not ours: SQLite, `lib/pq`, and `pgx` accept multi-statement input by default; `go-sql-driver/mysql` needs `?multiStatements=true` in the DSN. And the hash is over raw bytes, so a CRLF-vs-LF flip across machines reads as drift. Keep line endings consistent.

Full option list in [godoc](https://pkg.go.dev/github.com/etkecc/go-kit/migrater).

## License

GNU LGPL-3.0. See [../LICENSE](../LICENSE).
