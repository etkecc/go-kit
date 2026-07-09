# crypter/yaml

[![Go Reference](https://pkg.go.dev/badge/github.com/etkecc/go-kit.svg)](https://pkg.go.dev/github.com/etkecc/go-kit/crypter/yaml)

Encrypt the secret values *inside* a YAML file, in place, and leave everything else exactly as it was: keys, structure, non-secret values, and your comments. Point it at a predicate that says which keys hold secrets, and it walks the document encrypting only those, wrapping each in `ENCv1[...]`. Decrypt runs the walk in reverse.

The point is a config file you can still read and diff: the secrets are sealed, everything around them stays plaintext, and the comments are right where you left them.

## Read this before you `go get`: it's a separate module

`crypter/yaml` lives inside the `crypter/` directory, but it is **not** part of the `crypter` package or the root module. It's its own Go module with its own `go.mod`, and it pulls in `yaml.v3`:

```go
go get github.com/etkecc/go-kit/crypter/yaml
```

The directory tree says "child of crypter." The module boundary says "island." Both are true at once, and that split is deliberate: the YAML walk needs `yaml.v3`, and the root go-kit module is dependency-free on purpose. Park the yaml dependency out here and `go get github.com/etkecc/go-kit` keeps pulling zero deps. So do **not** `go mod tidy` these two together hoping to merge them, you'd drag yaml.v3 into everyone's root `go.sum` and undo the whole point.

The crypto primitive is injected through a local `Crypter` interface, which `*crypter.Crypter` satisfies structurally. That's why this module imports nothing from its parent: the interface is the seam that keeps the island an island.

```go
import (
    "github.com/etkecc/go-kit/crypter"
    cryptyaml "github.com/etkecc/go-kit/crypter/yaml"
)

c, _ := crypter.New(os.Getenv("SECRET_KEY"))

isSecret := func(key string) bool {
    return key == "password" || key == "token" || key == "api_key"
}

sealed, err := cryptyaml.EncryptBytes(raw, isSecret, c)   // encrypt secret-keyed scalars
plain, err  := cryptyaml.DecryptBytes(sealed, c)          // reverse
```

## The rules of the walk

- **Encrypts** scalar string values reached through a key your `isSecret` matches. Under a secret key: a scalar gets sealed, a sequence gets every leaf sealed (recursing nested sequences), and a mapping gets walked **by its own keys** rather than blanket-encrypted, so non-secret inner entries stay plaintext.
- **Skips** non-string scalars, YAML 1.1 booleans (`yes`/`no`/`on`/`off`/`true`/`false`, any case: encrypting one would flip its meaning on the next load), template expressions (`{{ ... }}`), and anything already tagged. Skipped values are counted, not touched.
- **Never decrypts keys.** An encrypted key name could rewrite the document's structure, so key names are left alone in both directions.
- **Comments survive both ways.** The walk runs on the parsed node tree and yaml.v3 re-marshals head, line, and foot comments intact. A file with nothing to change comes back byte-for-byte identical.
- **Fails loud, never leaks.** A tagged scalar that won't decrypt, or that decrypts to something still tagged (double-encryption), returns `ErrYAMLDecrypt` rather than handing you ciphertext and calling it a day.

A nil `isSecret` is `ErrNilPredicate`. Pass an optional `*Stats` to `EncryptBytes` to count what got sealed versus skipped; the `Stats` shape and the rest live in [godoc](https://pkg.go.dev/github.com/etkecc/go-kit/crypter/yaml).

## License

GNU LGPL-3.0. See [../../LICENSE](../../LICENSE).
