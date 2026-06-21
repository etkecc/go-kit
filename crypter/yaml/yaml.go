// Package yaml encrypts and decrypts string values inside YAML documents,
// in place, using an injected crypter.
//
// EncryptBytes walks a document and encrypts the scalar string values whose
// mapping key satisfies a caller-supplied predicate. DecryptBytes walks a
// document and decrypts every value that is already tagged as encrypted. In both
// directions the document structure, non-secret keys, and non-string scalars are
// preserved.
//
// # The island
//
// This is a separate Go module (github.com/etkecc/go-kit/crypter/yaml). Its
// go.mod requires only yaml.v3 and imports nothing from the parent go-kit module,
// so `go get github.com/etkecc/go-kit` keeps pulling zero dependencies. The YAML
// walk needs yaml.v3, so it lives here rather than in the dependency-free root.
//
// The crypto primitive is injected through the Crypter interface, which
// *github.com/etkecc/go-kit/crypter.Crypter satisfies structurally. Keep it an
// interface: making EncryptBytes/DecryptBytes methods on the concrete crypter
// type would pull yaml.v3 into the root module's go.sum and end its zero-dep root.
//
// # Comments and formatting
//
// The walk operates on the parsed node tree, so yaml.v3 preserves head, line, and
// foot comments across a re-marshal. Scalar quoting may be normalized: encrypted
// values are forced to double-quoted style, and decrypted values are reset to the
// style yaml.v3 picks for the content. A document with nothing to change is
// returned byte-for-byte unchanged via the empty-input and fast-path short-circuits.
package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"
)

// Crypter is the encryption primitive the walk operates over.
// *github.com/etkecc/go-kit/crypter.Crypter satisfies it.
//
// The walk depends on this interface rather than the concrete type so the module
// imports nothing from the parent go-kit (see the package doc). StartTag returns
// the prefix that marks an encrypted value and drives the DecryptBytes fast-path.
type Crypter interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
	IsEncrypted(string) bool
	StartTag() string
}

// Stats counts the outcome of an EncryptBytes walk. Encrypted is the number of
// scalar values newly encrypted on this pass; Skipped is the number that matched
// a secret key but were already encrypted (the idempotent re-run case). Values
// skipped for any other reason, such as non-string scalars, YAML 1.1 boolean
// literals, or template expressions, are not counted in either field.
type Stats struct {
	Encrypted int
	Skipped   int
}

var (
	// ErrYAMLDecode is returned when YAML parsing fails.
	ErrYAMLDecode = errors.New("crypter/yaml: yaml decode failed")

	// ErrYAMLEncode is returned when YAML re-encoding fails.
	ErrYAMLEncode = errors.New("crypter/yaml: yaml encode failed")

	// ErrYAMLDecrypt is returned when a scalar looks encrypted but cannot be
	// decrypted, or decrypts to a value that itself still looks encrypted.
	ErrYAMLDecrypt = errors.New("crypter/yaml: yaml decrypt failed")

	// ErrNilPredicate is returned by EncryptBytes when isSecret is nil. A nil
	// predicate is a caller error, not a "nothing is secret" default: treating it
	// as the latter would silently leave every value in plaintext, so the walk
	// fails before it starts. It returns an error rather than panicking, because a
	// library must not panic on caller input.
	ErrNilPredicate = errors.New("crypter/yaml: nil isSecret predicate")
)

// yamlBoolLiterals are the YAML 1.1 boolean spellings. yaml.v3 follows YAML 1.2
// and resolves them as strings, but a YAML 1.1 consumer reads them back as
// booleans, so encrypting one would flip its meaning on the next load once it is
// decrypted to a bare scalar. They are never encrypted.
var yamlBoolLiterals = map[string]bool{
	"yes": true, "no": true,
	"on": true, "off": true,
	"true": true, "false": true,
}

// EncryptBytes walks YAML document b and encrypts the string scalar values
// reached through a mapping key that satisfies isSecret. A matched key whose value
// is a scalar is encrypted directly; one whose value is a sequence has every
// scalar leaf encrypted (a secret spread across a list, recursing nested
// sequences); one whose value is a mapping is walked by its own keys instead,
// since the nested entries carry their own names to match. The document structure,
// comments, non-secret keys, and non-string scalars are preserved; scalar quoting
// may be normalized (see the package doc).
//
// A value is left untouched if it is a YAML 1.1 boolean literal, a template
// expression (contains "{{"), or already encrypted. Encrypted scalars are written
// in double-quoted style so the ENCv1[...] brackets are never read as flow syntax.
//
// The optional stats argument, when given, receives the per-value counts. The
// variadic form lets callers that do not want counts omit it.
//
// isSecret must not be nil (returns ErrNilPredicate). The crypter c must be
// non-nil: the walk does not re-check it, because a typed nil inside an interface
// cannot be detected reliably, so guard it upstream.
func EncryptBytes(b []byte, isSecret func(key string) bool, c Crypter, stats ...*Stats) ([]byte, error) {
	if isSecret == nil {
		return nil, ErrNilPredicate
	}
	if len(b) == 0 {
		return b, nil
	}

	var n yamlv3.Node
	if err := yamlv3.Unmarshal(b, &n); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrYAMLDecode, err)
	}
	if n.Kind == 0 {
		// Empty or whitespace-only input unmarshals to a null node; return it
		// unchanged rather than re-encoding as "null\n".
		return b, nil
	}

	w := &encryptWalk{c: c, isSecret: isSecret}
	if len(stats) > 0 {
		w.stats = stats[0]
	}
	if err := w.walk(&n); err != nil {
		return nil, err
	}

	out, err := yamlv3.Marshal(&n)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrYAMLEncode, err)
	}
	return out, nil
}

// encryptWalk holds the per-call state (crypter, predicate, optional counters) so
// one Crypter can drive concurrent EncryptBytes calls without shared mutation.
type encryptWalk struct {
	c        Crypter
	isSecret func(key string) bool
	stats    *Stats // optional; nil when the caller does not want counters
}

// walk routes mappings to mappingValues and recurses into document and sequence
// nodes. Scalars are encrypted only when reached through a matching mapping key;
// aliases resolve at their anchor.
func (w *encryptWalk) walk(n *yamlv3.Node) error {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yamlv3.MappingNode:
		return w.mappingValues(n)
	case yamlv3.DocumentNode, yamlv3.SequenceNode:
		for i := range n.Content {
			if err := w.walk(n.Content[i]); err != nil {
				return err
			}
		}
	case yamlv3.ScalarNode, yamlv3.AliasNode:
		// A scalar not under a secret key is never encrypted; secret values are
		// handled by encryptSecret. Aliases resolve at their anchor.
	}
	return nil
}

// mappingValues steps through the [key0,val0, key1,val1, ...] pairs. A value under
// a secret-matched key goes to encryptSecret; every other value is walked so
// nested mappings are reached.
func (w *encryptWalk) mappingValues(n *yamlv3.Node) error {
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if w.isSecret(key.Value) {
			if err := w.encryptSecret(val); err != nil {
				return err
			}
			continue
		}
		if err := w.walk(val); err != nil {
			return err
		}
	}
	return nil
}

// encryptSecret encrypts the value of a secret-matched key. A scalar is encrypted
// directly; a sequence has every scalar leaf encrypted, recursing through nested
// sequences (a secret spread across a list). A mapping is walked by its own keys
// instead, so its entries are matched on their own names rather than blanket
// encrypted, which keeps structured sub-data (where some entries may not be secret)
// judged correctly.
func (w *encryptWalk) encryptSecret(n *yamlv3.Node) error {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yamlv3.ScalarNode:
		return w.scalar(n)
	case yamlv3.SequenceNode:
		for i := range n.Content {
			if err := w.encryptSecret(n.Content[i]); err != nil {
				return err
			}
		}
		return nil
	default:
		return w.walk(n)
	}
}

// scalar encrypts a !!str node in place, skipping non-strings, YAML 1.1 boolean
// literals, template expressions, and already-encrypted values. Already-encrypted
// values are counted as Skipped, not re-wrapped.
func (w *encryptWalk) scalar(n *yamlv3.Node) error {
	if n.Tag != "!!str" {
		return nil
	}
	if yamlBoolLiterals[strings.ToLower(n.Value)] {
		return nil
	}
	if strings.Contains(n.Value, "{{") {
		return nil
	}
	if w.c.IsEncrypted(n.Value) {
		if w.stats != nil {
			w.stats.Skipped++
		}
		return nil
	}
	enc, err := w.c.Encrypt(n.Value)
	if err != nil {
		return err
	}
	n.Value = enc
	n.Tag = "!!str"
	n.Style = yamlv3.DoubleQuotedStyle
	if w.stats != nil {
		w.stats.Encrypted++
	}
	return nil
}

// DecryptBytes walks YAML document b and decrypts encrypted scalar values,
// returning re-encoded YAML. Mapping keys are never decrypted even if one carries
// an encrypted tag, so an encrypted key name cannot rewrite the document structure.
//
// Fast-path: a document with no StartTag anywhere is returned unchanged without
// parsing, so plaintext files cost nothing and keep their comments byte-for-byte.
//
// Strict: a scalar that starts with StartTag but cannot be decrypted (wrong key,
// corrupted ciphertext), or that decrypts to a value still looking encrypted
// (double-encryption), fails with ErrYAMLDecrypt rather than leaking ciphertext
// into the output.
//
// The crypter c must be non-nil; see EncryptBytes for why.
func DecryptBytes(b []byte, c Crypter) ([]byte, error) {
	if len(b) == 0 {
		return b, nil
	}
	if !bytes.Contains(b, []byte(c.StartTag())) {
		return b, nil
	}

	var n yamlv3.Node
	if err := yamlv3.Unmarshal(b, &n); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrYAMLDecode, err)
	}
	if n.Kind == 0 {
		// A StartTag substring with no parseable document unmarshals to a null
		// node; return it unchanged rather than re-encoding as "null\n".
		return b, nil
	}

	w := &decryptWalk{c: c}
	if err := w.walk(&n); err != nil {
		return nil, err
	}

	out, err := yamlv3.Marshal(&n)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrYAMLEncode, err)
	}
	return out, nil
}

// decryptWalk holds the injected crypter for the decrypt traversal.
type decryptWalk struct {
	c Crypter
}

// walk decrypts scalar values in place. Mappings skip their keys; document and
// sequence nodes recurse into every child; alias nodes are skipped, since the
// anchor they point to is decrypted at its own position and Marshal resolves
// aliases inline.
func (w *decryptWalk) walk(n *yamlv3.Node) error {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yamlv3.MappingNode:
		return w.mappingValues(n)
	case yamlv3.ScalarNode:
		return w.scalar(n)
	case yamlv3.DocumentNode, yamlv3.SequenceNode:
		return w.children(n)
	case yamlv3.AliasNode:
		return nil
	default:
		return w.children(n)
	}
}

// children recurses into every child of n.
func (w *decryptWalk) children(n *yamlv3.Node) error {
	for i := range n.Content {
		if err := w.walk(n.Content[i]); err != nil {
			return err
		}
	}
	return nil
}

// mappingValues recurses only into the values of a mapping (indices i+1), never
// the keys.
func (w *decryptWalk) mappingValues(n *yamlv3.Node) error {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if err := w.walk(n.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// scalar decrypts a !!str node in place if its value is tagged as encrypted.
// Style is reset to 0 so yaml.v3 picks the natural representation: a block scalar
// for multi-line values such as PEM-encoded keys, plain for simple strings. A
// value still encrypted after Decrypt returns is treated as a hard failure.
func (w *decryptWalk) scalar(n *yamlv3.Node) error {
	if n.Tag != "!!str" || !w.c.IsEncrypted(n.Value) {
		return nil
	}
	plain, err := w.c.Decrypt(n.Value)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrYAMLDecrypt, err)
	}
	if w.c.IsEncrypted(plain) {
		return ErrYAMLDecrypt
	}
	n.Value = plain
	n.Tag = "!!str"
	n.Style = 0
	return nil
}
