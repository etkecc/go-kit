package yaml

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	yamlv3 "gopkg.in/yaml.v3"
)

// --- test crypter ---------------------------------------------------------
//
// A self-contained reversible stub so the walk can be tested without importing
// the parent go-kit crypter (which would put a require on the parent into this
// module's go.mod and break the zero-dependency island). The real AES-GCM crypto
// is covered by the root crypter package's own tests; these tests cover the walk.
//
// StartTag deliberately differs from the real "ENCv1[" so the decrypt fast-path
// is proven to go through the injected StartTag() method rather than a hardcoded
// constant. The payload is base64url so it never contains YAML metacharacters,
// "{{", or the bool-literal spellings.

const (
	stubStart = "STUBv1["
	stubEnd   = "]"
)

var errStubDecrypt = errors.New("stub: cannot decrypt")

type stubCrypter struct{}

func (stubCrypter) StartTag() string { return stubStart }

func (stubCrypter) IsEncrypted(s string) bool {
	return len(s) > len(stubStart)+len(stubEnd) &&
		strings.HasPrefix(s, stubStart) &&
		strings.HasSuffix(s, stubEnd)
}

func (c stubCrypter) Encrypt(s string) (string, error) {
	if c.IsEncrypted(s) {
		return s, nil
	}
	return stubStart + base64.RawURLEncoding.EncodeToString([]byte(s)) + stubEnd, nil
}

func (c stubCrypter) Decrypt(s string) (string, error) {
	if !c.IsEncrypted(s) {
		return s, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(stubStart) : len(s)-len(stubEnd)])
	if err != nil {
		return "", errStubDecrypt
	}
	return string(raw), nil
}

// secretKeyPatterns mirrors the shape of the real consumer predicates:
// case-insensitive substring match. The patterns live in the consumer, never in
// this package; this is a test stand-in.
var secretKeyPatterns = []string{"password", "secret", "token", "api_key", "_key", "privkey", "phrase"}

func testIsSecret(key string) bool {
	lower := strings.ToLower(key)
	for _, p := range secretKeyPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func mustEncrypt(t *testing.T, s string) string {
	t.Helper()
	out, err := stubCrypter{}.Encrypt(s)
	if err != nil {
		t.Fatalf("stub Encrypt(%q): %v", s, err)
	}
	if out == s {
		t.Fatalf("stub Encrypt(%q) did not change the value", s)
	}
	return out
}

// --- EncryptBytes: what gets encrypted ------------------------------------

func TestEncryptBytes_PatternMatchAndPreservation(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	in := []byte(strings.Join([]string{
		"admin_password: secret123",
		"client_secret: hunter2",
		"service_domain: example.com",
		"service_port: 8448",
		"service_enabled: true",
		"",
	}, "\n"))

	out, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	s := string(out)

	if strings.Count(s, stubStart) != 2 {
		t.Errorf("expected 2 encrypted values, got %d; output: %q", strings.Count(s, stubStart), s)
	}
	for _, plain := range []string{"secret123", "hunter2"} {
		if strings.Contains(s, plain) {
			t.Errorf("plaintext secret %q leaked; output: %q", plain, s)
		}
	}
	for _, keep := range []string{"example.com", "8448", "true"} {
		if !strings.Contains(s, keep) {
			t.Errorf("non-secret value %q not preserved; output: %q", keep, s)
		}
	}
}

func TestEncryptBytes_NestedMapping(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	in := []byte(strings.Join([]string{
		"push_apps:",
		"  host.example.com:",
		"    type: gcm",
		"    api_key: AIzaPlainKey",
		"",
	}, "\n"))

	out, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	s := string(out)

	if !strings.Contains(s, stubStart) {
		t.Errorf("expected nested api_key encrypted; output: %q", s)
	}
	if strings.Contains(s, "AIzaPlainKey") {
		t.Errorf("nested plaintext leaked; output: %q", s)
	}
	if !strings.Contains(s, "gcm") {
		t.Errorf("non-secret sibling not preserved; output: %q", s)
	}
}

func TestEncryptBytes_SequenceOfMappings(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	in := []byte(strings.Join([]string{
		"users:",
		"  - name: alice",
		"    password: alicepass",
		"  - name: bob",
		"    password: bobpass",
		"    role: admin",
		"",
	}, "\n"))

	out, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	s := string(out)

	if strings.Count(s, stubStart) != 2 {
		t.Errorf("expected 2 list passwords encrypted, got %d; output: %q", strings.Count(s, stubStart), s)
	}
	for _, keep := range []string{"alice", "bob", "admin"} {
		if !strings.Contains(s, keep) {
			t.Errorf("non-secret %q not preserved; output: %q", keep, s)
		}
	}
}

// --- EncryptBytes: secret-keyed sequences ---------------------------------

func TestEncryptBytes_SecretKeyedScalarSequence(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// A secret spread across a list: every scalar leaf is encrypted.
	in := []byte("password:\n  - hunter2\n  - swordfish\n")
	var st Stats
	out, err := EncryptBytes(in, testIsSecret, c, &st)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if strings.Count(string(out), stubStart) != 2 || st.Encrypted != 2 {
		t.Errorf("expected both list scalars encrypted; count=%d stats=%+v out=%q",
			strings.Count(string(out), stubStart), st, out)
	}
	for _, plain := range []string{"hunter2", "swordfish"} {
		if strings.Contains(string(out), plain) {
			t.Errorf("plaintext %q leaked from sequence; out=%q", plain, out)
		}
	}
}

func TestEncryptBytes_SecretKeyedSequenceLeafGuards(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// The leaf-level guard runs per element: a non-string scalar and a YAML 1.1
	// bool literal in the list are left alone; only the real string encrypts.
	in := []byte("secret_list:\n  - 123\n  - realsecret\n  - yes\n")
	var st Stats
	out, err := EncryptBytes(in, testIsSecret, c, &st)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if strings.Count(string(out), stubStart) != 1 || st.Encrypted != 1 {
		t.Errorf("expected only the string leaf encrypted; count=%d stats=%+v out=%q",
			strings.Count(string(out), stubStart), st, out)
	}
	if strings.Contains(string(out), "realsecret") {
		t.Errorf("string leaf not encrypted; out=%q", out)
	}
	if !strings.Contains(string(out), "123") || !strings.Contains(string(out), "yes") {
		t.Errorf("non-string / bool-literal leaves must be preserved; out=%q", out)
	}
}

func TestEncryptBytes_SecretKeyedNestedSequence(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	in := []byte("password:\n  - - nested1\n    - nested2\n  - flat\n")
	var st Stats
	out, err := EncryptBytes(in, testIsSecret, c, &st)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if strings.Count(string(out), stubStart) != 3 || st.Encrypted != 3 {
		t.Errorf("expected all 3 nested-sequence scalars encrypted; count=%d stats=%+v out=%q",
			strings.Count(string(out), stubStart), st, out)
	}
}

func TestEncryptBytes_SecretKeyedSequenceOfMappings_RecursesByInnerKeys(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// A sequence of mappings under a secret key is NOT blanket-encrypted: each
	// mapping is matched by its own keys, so a non-secret entry stays plaintext.
	in := []byte("secrets:\n  - host: db.example.com\n    password: dbpass\n")
	out, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	s := string(out)
	if strings.Count(s, stubStart) != 1 {
		t.Errorf("expected only the inner password encrypted; out=%q", s)
	}
	if !strings.Contains(s, "db.example.com") {
		t.Errorf("non-secret inner key (host) must stay plaintext; out=%q", s)
	}
	if strings.Contains(s, "dbpass") {
		t.Errorf("inner secret should be encrypted; out=%q", s)
	}
}

func TestRoundTrip_SecretKeyedSequence(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	in := []byte("api_key:\n  - keyA\n  - keyB\nservice_domain: example.com\n")
	enc, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if strings.Contains(string(enc), "keyA") || strings.Contains(string(enc), "keyB") {
		t.Fatalf("sequence secrets present after encrypt; out=%q", enc)
	}

	dec, err := DecryptBytes(enc, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	s := string(dec)
	for _, want := range []string{"keyA", "keyB", "example.com"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q after round-trip; out=%q", want, s)
		}
	}
	if strings.Contains(s, stubStart) {
		t.Errorf("ciphertext remains after decrypt; out=%q", s)
	}
}

// --- EncryptBytes: what gets skipped --------------------------------------

func TestEncryptBytes_Skips(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	cases := []struct {
		name string
		in   string
	}{
		{"jinja template", "admin_password: \"{{ vault_admin_password }}\"\n"},
		{"jinja unquoted", "api_token: {{ token_ref }}\n"},
		{"non-string int", "secret_port: 8448\n"},
		{"non-string bool", "secret_enabled: true\n"},
		{"yaml 1.1 yes", "secret_flag: yes\n"},
		{"yaml 1.1 no", "password_set: no\n"},
		{"yaml 1.1 on", "token_on: on\n"},
		{"yaml 1.1 off", "secret_off: off\n"},
		{"yaml 1.1 True mixed-case", "client_secret: True\n"},
		{"non-secret key", "service_domain: keepme\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := EncryptBytes([]byte(tc.in), testIsSecret, c)
			if err != nil {
				t.Fatalf("EncryptBytes: %v", err)
			}
			if strings.Contains(string(out), stubStart) {
				t.Errorf("expected no encryption for %s; output: %q", tc.name, string(out))
			}
		})
	}
}

func TestEncryptBytes_NilPredicate(t *testing.T) {
	t.Parallel()
	_, err := EncryptBytes([]byte("admin_password: secret\n"), nil, stubCrypter{})
	if !errors.Is(err, ErrNilPredicate) {
		t.Fatalf("expected ErrNilPredicate, got %v", err)
	}
}

func TestEncryptBytes_EmptyAndNullNode(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	out, err := EncryptBytes(nil, testIsSecret, c)
	if err != nil || out != nil {
		t.Fatalf("nil input: got (%v, %v), want (nil, nil)", out, err)
	}

	out, err = EncryptBytes([]byte{}, testIsSecret, c)
	if err != nil || len(out) != 0 {
		t.Fatalf("empty input: got (%q, %v), want (empty, nil)", out, err)
	}

	// Whitespace-only unmarshals to a null node: returned unchanged, not "null\n".
	in := []byte("   \n")
	out, err = EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("whitespace input: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("null node not returned unchanged; got %q", out)
	}
}

func TestEncryptBytes_InvalidYAML(t *testing.T) {
	t.Parallel()
	_, err := EncryptBytes([]byte("a: [\nb: c\n"), testIsSecret, stubCrypter{})
	if !errors.Is(err, ErrYAMLDecode) {
		t.Fatalf("expected ErrYAMLDecode, got %v", err)
	}
}

// --- EncryptBytes: stats and idempotency ----------------------------------

func TestEncryptBytes_Stats(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}
	already := mustEncrypt(t, "preexisting")

	// One fresh secret (counts Encrypted), one already-encrypted (counts Skipped),
	// plus a bool literal and a non-string under secret keys (counted in NEITHER).
	in := []byte(strings.Join([]string{
		"admin_password: freshpass",
		"client_secret: \"" + already + "\"",
		"secret_flag: yes",
		"secret_port: 8448",
		"",
	}, "\n"))

	var st Stats
	if _, err := EncryptBytes(in, testIsSecret, c, &st); err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if st.Encrypted != 1 {
		t.Errorf("Encrypted = %d, want 1", st.Encrypted)
	}
	if st.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (only already-encrypted counts, not bool/non-string)", st.Skipped)
	}
}

func TestEncryptBytes_NoStatsArgDoesNotPanic(t *testing.T) {
	t.Parallel()
	out, err := EncryptBytes([]byte("admin_password: secret\n"), testIsSecret, stubCrypter{})
	if err != nil {
		t.Fatalf("EncryptBytes without stats: %v", err)
	}
	if !strings.Contains(string(out), stubStart) {
		t.Fatalf("expected encryption to occur; output: %q", out)
	}
}

func TestEncryptBytes_IdempotentDoublePass(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	in := []byte("admin_password: supersecret\nservice_port: 587\n")

	var first Stats
	once, err := EncryptBytes(in, testIsSecret, c, &first)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.Encrypted != 1 || first.Skipped != 0 {
		t.Fatalf("first pass stats = %+v, want {1 0}", first)
	}

	var second Stats
	twice, err := EncryptBytes(once, testIsSecret, c, &second)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.Encrypted != 0 || second.Skipped != 1 {
		t.Errorf("second pass stats = %+v, want {0 1}", second)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("second pass changed bytes (double-wrap?):\nfirst:  %q\nsecond: %q", once, twice)
	}
}

func TestEncryptBytes_QuotedStyleSurvivesReparse(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	out, err := EncryptBytes([]byte("admin_password: secret\n"), testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	// The ciphertext brackets must be quoted so re-parsing yields the scalar, not
	// a flow sequence or a parse error.
	var m map[string]string
	if err := yamlv3.Unmarshal(out, &m); err != nil {
		t.Fatalf("encrypted output failed to re-parse: %v\noutput: %q", err, out)
	}
	if !c.IsEncrypted(m["admin_password"]) {
		t.Errorf("re-parsed value is not a single encrypted scalar: %q", m["admin_password"])
	}
}

// --- DecryptBytes ---------------------------------------------------------

func TestDecryptBytes_FastPathNoTag(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// No StartTag anywhere: returned byte-for-byte, comments and quoting intact.
	in := []byte("# top comment\nservice_domain: example.com # inline\nport: 8448\n")
	out, err := DecryptBytes(in, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("fast-path must return input unchanged;\n got: %q\nwant: %q", out, in)
	}
}

func TestDecryptBytes_EmptyInput(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	out, err := DecryptBytes(nil, c)
	if err != nil || out != nil {
		t.Fatalf("nil input: got (%v, %v), want (nil, nil)", out, err)
	}
	out, err = DecryptBytes([]byte{}, c)
	if err != nil || len(out) != 0 {
		t.Fatalf("empty input: got (%q, %v), want (empty, nil)", out, err)
	}
}

func TestDecryptBytes_DoesNotDecryptKeys(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	keyEnc := mustEncrypt(t, "looks-like-a-key")
	valEnc := mustEncrypt(t, "real-value")

	in := []byte("root:\n  \"" + keyEnc + "\": plain\n  k2: \"" + valEnc + "\"\n")
	out, err := DecryptBytes(in, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	s := string(out)

	if !strings.Contains(s, keyEnc) {
		t.Errorf("encrypted key must stay encrypted; output: %q", s)
	}
	if !strings.Contains(s, "real-value") {
		t.Errorf("value must be decrypted; output: %q", s)
	}
	if strings.Contains(s, valEnc) {
		t.Errorf("encrypted value should be gone; output: %q", s)
	}
}

func TestDecryptBytes_NestedAndSequences(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	v1 := mustEncrypt(t, "s1")
	v2 := mustEncrypt(t, "s2")
	v3 := mustEncrypt(t, "s3")

	in := []byte(strings.Join([]string{
		"a: \"" + v1 + "\"",
		"b:",
		"  c: \"" + v2 + "\"",
		"  d:",
		"    - x",
		"    - \"" + v3 + "\"",
		"e: 42",
		"f: true",
		"g: null",
		"",
	}, "\n"))

	out, err := DecryptBytes(in, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	s := string(out)

	for _, want := range []string{"s1", "s2", "s3"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q decrypted; output: %q", want, s)
		}
	}
	for _, enc := range []string{v1, v2, v3} {
		if strings.Contains(s, enc) {
			t.Errorf("ciphertext %q should be gone; output: %q", enc, s)
		}
	}
	if !strings.Contains(s, "e: 42") || !strings.Contains(s, "f: true") {
		t.Errorf("non-secret scalars not preserved; output: %q", s)
	}
}

func TestDecryptBytes_SequenceRoot(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	v1 := mustEncrypt(t, "aa")
	v2 := mustEncrypt(t, "bb")

	in := []byte(strings.Join([]string{
		"- \"" + v1 + "\"",
		"- plain",
		"- nested:",
		"    k: \"" + v2 + "\"",
		"",
	}, "\n"))

	out, err := DecryptBytes(in, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	s := string(out)
	for _, want := range []string{"aa", "plain", "bb"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in output; got %q", want, s)
		}
	}
}

func TestDecryptBytes_MixedPlaintextAndCiphertext(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	enc := mustEncrypt(t, "ciphered")
	in := []byte("a: plainvalue\nb: \"" + enc + "\"\n")

	out, err := DecryptBytes(in, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "plainvalue") {
		t.Errorf("plaintext untouched expected; output: %q", s)
	}
	if !strings.Contains(s, "ciphered") || strings.Contains(s, enc) {
		t.Errorf("ciphertext should decrypt; output: %q", s)
	}
}

func TestDecryptBytes_InvalidYAMLWithTag(t *testing.T) {
	t.Parallel()
	// Carries the StartTag so the fast-path falls through to a parse that fails.
	in := []byte("a: [\n" + stubStart + "fake" + stubEnd)
	_, err := DecryptBytes(in, stubCrypter{})
	if !errors.Is(err, ErrYAMLDecode) {
		t.Fatalf("expected ErrYAMLDecode, got %v", err)
	}
}

func TestDecryptBytes_UndecryptablePayload(t *testing.T) {
	t.Parallel()
	// Tagged, parseable, but the payload is not valid base64: strict failure.
	in := []byte("a: \"" + stubStart + "***" + stubEnd + "\"\n")
	_, err := DecryptBytes(in, stubCrypter{})
	if !errors.Is(err, ErrYAMLDecrypt) {
		t.Fatalf("expected ErrYAMLDecrypt, got %v", err)
	}
}

func TestDecryptBytes_DoubleEncryptedFails(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// A value that decrypts to something still tagged as encrypted must error,
	// never leak the inner ciphertext.
	once := mustEncrypt(t, "innersecret")
	twice := stubStart + base64.RawURLEncoding.EncodeToString([]byte(once)) + stubEnd

	in := []byte("a: \"" + twice + "\"\n")
	_, err := DecryptBytes(in, c)
	if !errors.Is(err, ErrYAMLDecrypt) {
		t.Fatalf("expected ErrYAMLDecrypt on double-encrypted value, got %v", err)
	}
}

// --- round-trips and comment behavior -------------------------------------

func TestRoundTrip_StructureAndValues(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	in := []byte(strings.Join([]string{
		"admin_password: mypassword",
		"client_secret: mysecret",
		"service_domain: example.com",
		"db:",
		"  host: localhost",
		"  password: dbpass",
		"  port: 5432",
		"",
	}, "\n"))

	enc, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if strings.Contains(string(enc), "mypassword") || strings.Contains(string(enc), "dbpass") {
		t.Fatalf("secrets present after encrypt; output: %q", enc)
	}

	dec, err := DecryptBytes(enc, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	s := string(dec)
	for _, want := range []string{"mypassword", "mysecret", "dbpass", "example.com", "localhost", "5432"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q after round-trip; output: %q", want, s)
		}
	}
	if strings.Contains(s, stubStart) {
		t.Errorf("ciphertext remains after decrypt; output: %q", s)
	}
}

func TestRoundTrip_BlockScalarMultiline(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	pem := "-----BEGIN KEY-----\nline1\nline2\n-----END KEY-----\n"
	in := []byte("privkey_contents: |\n  -----BEGIN KEY-----\n  line1\n  line2\n  -----END KEY-----\n")

	enc, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	if !strings.Contains(string(enc), stubStart) {
		t.Fatalf("multi-line secret not encrypted; output: %q", enc)
	}

	dec, err := DecryptBytes(enc, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	var m map[string]string
	if err := yamlv3.Unmarshal(dec, &m); err != nil {
		t.Fatalf("decrypted output failed to parse: %v", err)
	}
	if m["privkey_contents"] != pem {
		t.Errorf("multi-line value not restored:\n got: %q\nwant: %q", m["privkey_contents"], pem)
	}
}

func TestComments_PreservedAcrossEncryptAndDecrypt(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	withComments := []byte("# header comment\nadmin_password: secret # trailing\nservice_domain: example.com # side\n")

	// Encrypt re-marshals through the node tree, which yaml.v3 preserves comments
	// across. The secret is encrypted; the comments survive.
	enc, err := EncryptBytes(withComments, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	se := string(enc)
	if !strings.Contains(se, stubStart) {
		t.Fatalf("expected the secret encrypted; output: %q", se)
	}
	for _, cm := range []string{"# header comment", "# trailing", "# side"} {
		if !strings.Contains(se, cm) {
			t.Errorf("comment %q must survive encrypt; output: %q", cm, se)
		}
	}

	// Decrypt re-marshals too: comments survive and the value is restored.
	dec, err := DecryptBytes(enc, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	sd := string(dec)
	if strings.Contains(sd, stubStart) || !strings.Contains(sd, "secret") {
		t.Errorf("value not restored on decrypt; output: %q", sd)
	}
	for _, cm := range []string{"# header comment", "# trailing", "# side"} {
		if !strings.Contains(sd, cm) {
			t.Errorf("comment %q must survive decrypt; output: %q", cm, sd)
		}
	}
}

func TestDecryptBytes_FastPathPreservesBytes(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// All-plaintext document with comments and varied quoting: the no-tag
	// fast-path returns it byte-for-byte, the strongest preservation guarantee.
	in := []byte("# top\nservice_domain: example.com # inline\nlist:\n  - one\n  - 'two'\n")
	out, err := DecryptBytes(in, c)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("fast-path must return input unchanged;\n got: %q\nwant: %q", out, in)
	}
}

func TestEncryptBytes_BoolLiteralVsLookalikeString(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// "yes" is a YAML 1.1 bool literal (skipped); "notyes" is an ordinary string
	// under a secret key (encrypted). The positive control proves the skip is the
	// literal check, not a blanket pass on anything starting with the spelling.
	in := []byte("secret_a: yes\nsecret_b: notyes\n")
	out, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	s := string(out)
	if strings.Count(s, stubStart) != 1 {
		t.Errorf("expected exactly the lookalike string encrypted, got %d; output: %q", strings.Count(s, stubStart), s)
	}
	if strings.Contains(s, "notyes") {
		t.Errorf("lookalike string should be encrypted; output: %q", s)
	}
	if !strings.Contains(s, "yes") {
		t.Errorf("bool literal should be left as-is; output: %q", s)
	}
}

func TestDecryptBytes_InvalidYAMLNoTag_PassThrough(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// Invalid YAML, but no StartTag: the fast-path returns before the parser runs,
	// so there is no error and the bytes are unchanged.
	in := []byte("a: [\n")
	out, err := DecryptBytes(in, c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("expected pass-through; got %q", out)
	}
}

func TestEncryptBytes_ConcurrentSharedCrypter(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// One Crypter, many concurrent walks: the per-call walker struct must not
	// share state. Run under -race to make a data race a hard failure.
	in := []byte("admin_password: secret\nclient_secret: hunter2\nservice_domain: example.com\n")

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			var st Stats
			out, err := EncryptBytes(in, testIsSecret, c, &st)
			if err != nil {
				t.Errorf("concurrent EncryptBytes: %v", err)
				return
			}
			if strings.Count(string(out), stubStart) != 2 || st.Encrypted != 2 {
				t.Errorf("concurrent result drifted: count=%d stats=%+v", strings.Count(string(out), stubStart), st)
			}
		})
	}
	wg.Wait()
}

func TestAliasesDoNotBreakWalk(t *testing.T) {
	t.Parallel()
	c := stubCrypter{}

	// Anchor/alias on non-secret data: the walk must not error or corrupt the
	// document. Aliases resolve at their anchor; Marshal expands them inline.
	in := []byte(strings.Join([]string{
		"defaults: &def",
		"  region: eu",
		"site:",
		"  <<: *def",
		"  admin_password: sitepass",
		"",
	}, "\n"))

	enc, err := EncryptBytes(in, testIsSecret, c)
	if err != nil {
		t.Fatalf("EncryptBytes with alias: %v", err)
	}
	if !strings.Contains(string(enc), stubStart) {
		t.Errorf("secret alongside alias not encrypted; output: %q", enc)
	}

	dec, err := DecryptBytes(enc, c)
	if err != nil {
		t.Fatalf("DecryptBytes with alias: %v", err)
	}
	if !strings.Contains(string(dec), "sitepass") {
		t.Errorf("secret not restored through alias doc; output: %q", dec)
	}
}
