package crypter

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ansibleVaultBin = "/usr/bin/ansible-vault"

func TestNew_InvalidKeyLength(t *testing.T) {
	t.Parallel()

	_, err := New("short")
	if !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("expected ErrInvalidKeyLength, got %v", err)
	}
}

func TestNew_OK(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c == nil || c.aead == nil || c.nonceSize <= 0 {
		t.Fatalf("invalid crypter: %#v", c)
	}
}

func TestIsEncrypted_FastHeuristic(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}

	if c.IsEncrypted("") {
		t.Fatal("empty must not be encrypted")
	}
	if c.IsEncrypted(StartTag) {
		t.Fatalf("StartTag alone must not be encrypted")
	}
	if c.IsEncrypted("ENCv1") {
		t.Fatalf("prefix-like must not be encrypted")
	}
	if !c.IsEncrypted(StartTag + "x") {
		t.Fatalf("StartTag + payload must be encrypted by heuristic")
	}
}

func TestEncrypt_AlreadyTagged_ReturnsInput(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}

	in := StartTag + "not-really-base64" + EndTag
	out, err := c.Encrypt(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != in {
		t.Fatalf("expected unchanged, got %q", out)
	}
}

func TestDecrypt_Plaintext_FastReturn(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}

	in := "just a normal value"
	out, err := c.Decrypt(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != in {
		t.Fatalf("expected %q, got %q", in, out)
	}
}

func TestDecrypt_TaggedButMissingEndTag(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Decrypt(StartTag + "abcd") // no closing ]
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrInvalidCipherText) {
		t.Fatalf("expected ErrInvalidCipherText, got %v", err)
	}
}

func TestDecrypt_TaggedEmptyPayload(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Decrypt(StartTag + EndTag)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("expected ErrEmptyPayload, got %v", err)
	}
}

func TestDecrypt_Base64DecodeError_Wrapped(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Decrypt(StartTag + "abc*def" + EndTag) // invalid base64url char '*'
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrBase64Decode) {
		t.Fatalf("expected ErrBase64Decode, got %v", err)
	}
}

func TestEncryptDecrypt_RoundTrip_Various(t *testing.T) {
	t.Parallel()

	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}

	cases := []string{
		"",
		"a",
		"hello world",
		strings.Repeat("x", 100),
		"A-Za-z0-9_-",
		"line1\nline2\nline3",
	}

	for _, in := range cases {
		enc, err := c.Encrypt(in)
		if err != nil {
			t.Fatalf("Encrypt(%q) err: %v", in, err)
		}
		if enc == in {
			t.Fatalf("expected encrypted to differ for %q", in)
		}
		if !strings.HasPrefix(enc, StartTag) || !strings.HasSuffix(enc, EndTag) {
			t.Fatalf("missing tags: %q", enc)
		}

		out, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt(enc(%q)) err: %v", in, err)
		}
		if out != in {
			t.Fatalf("roundtrip mismatch: in=%q out=%q", in, out)
		}
	}
}

func TestDecrypt_AuthFail_Wrapped(t *testing.T) {
	t.Parallel()

	c1, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := New(strings.Repeat("z", 32))
	if err != nil {
		t.Fatal(err)
	}

	enc, err := c1.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c2.Decrypt(enc)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

func BenchmarkIsEncrypted_Plaintext(b *testing.B) {
	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		b.Fatal(err)
	}
	in := "this is plaintext"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = c.IsEncrypted(in)
	}
}

func BenchmarkIsEncrypted_EncryptedLike(b *testing.B) {
	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		b.Fatal(err)
	}
	in := StartTag + "x"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = c.IsEncrypted(in)
	}
}

func BenchmarkEncrypt_Short(b *testing.B) {
	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		b.Fatal(err)
	}
	in := "value"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := c.Encrypt(in)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncrypt_100(b *testing.B) {
	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		b.Fatal(err)
	}
	in := strings.Repeat("a", 100)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := c.Encrypt(in)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecrypt_PlaintextFastReturn(b *testing.B) {
	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		b.Fatal(err)
	}
	in := "this is plaintext"

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := c.Decrypt(in)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecrypt_Encrypted_Short(b *testing.B) {
	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		b.Fatal(err)
	}
	enc, err := c.Encrypt("value")
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := c.Decrypt(enc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecrypt_Encrypted_100_AlphaNumDashUnderscore(b *testing.B) {
	c, err := New(strings.Repeat("k", 32))
	if err != nil {
		b.Fatal(err)
	}

	in := strings.Repeat("Ab0_-zY9xQ", 10) // len=100
	if len(in) != 100 {
		b.Fatalf("bad input len: %d", len(in))
	}

	enc, err := c.Encrypt(in)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := c.Decrypt(enc)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVault_EncryptFile_100(b *testing.B) {
	passFile := mustVaultPassFile(b)
	defer os.Remove(passFile)

	in := strings.Repeat("Ab0_-zY9xQ", 10) // 100 chars
	if len(in) != 100 {
		b.Fatalf("bad input len: %d", len(in))
	}

	dir := b.TempDir()
	fpath := filepath.Join(dir, "secret.txt")

	encArgs := []string{"encrypt", "--vault-password-file", passFile, fpath}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := os.WriteFile(fpath, []byte(in), 0o600); err != nil {
			b.Fatal(err)
		}
		_, err := runAnsibleVault(encArgs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVault_ViewFile_Encrypted_100(b *testing.B) {
	passFile := mustVaultPassFile(b)
	defer os.Remove(passFile)

	in := strings.Repeat("Ab0_-zY9xQ", 10) // 100 chars
	if len(in) != 100 {
		b.Fatalf("bad input len: %d", len(in))
	}

	dir := b.TempDir()
	fpath := filepath.Join(dir, "secret.txt")

	// Pre-encrypt once outside the timing loop.
	if err := os.WriteFile(fpath, []byte(in), 0o600); err != nil {
		b.Fatal(err)
	}
	_, err := runAnsibleVault([]string{"encrypt", "--vault-password-file", passFile, fpath})
	if err != nil {
		b.Fatalf("pre-encrypt failed: %v", err)
	}

	viewArgs := []string{"view", "--vault-password-file", passFile, fpath}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		out, err := runAnsibleVault(viewArgs)
		if err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(bytes.TrimSpace(out), []byte(in)) {
			// `view` prints plaintext file content; should match exactly.
			b.Fatalf("view output mismatch: got %q want %q", string(out), in)
		}
	}
}

func BenchmarkVault_ViewFile_Plaintext_100_Overhead(b *testing.B) {
	// Measures overhead when attempting to view a plaintext file (expected to fail).
	passFile := mustVaultPassFile(b)
	defer os.Remove(passFile)

	in := strings.Repeat("Ab0_-zY9xQ", 10) // 100 chars

	dir := b.TempDir()
	fpath := filepath.Join(dir, "plain.txt")

	viewArgs := []string{"view", "--vault-password-file", passFile, fpath}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := os.WriteFile(fpath, []byte(in), 0o600); err != nil {
			b.Fatal(err)
		}
		_, _ = runAnsibleVault(viewArgs) // expected to fail; measure overhead
	}
}

func mustVaultPassFile(b *testing.B) (path string) {
	b.Helper()

	if _, err := os.Stat(ansibleVaultBin); err != nil {
		b.Skipf("ansible-vault not available at %s: %v", ansibleVaultBin, err)
	}
	if fi, err := os.Stat(ansibleVaultBin); err == nil && (fi.Mode()&0o111) == 0 {
		b.Skipf("ansible-vault not executable: %s", ansibleVaultBin)
	}

	dir := b.TempDir()
	path = filepath.Join(dir, "vault-pass.txt")
	if err := os.WriteFile(path, []byte("bench-pass-please-change\n"), 0o600); err != nil {
		b.Fatalf("write pass file: %v", err)
	}
	return path
}

func runAnsibleVault(args []string) ([]byte, error) {
	cmd := exec.Command(ansibleVaultBin, args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New("ansible-vault failed: " + msg)
	}
	return out.Bytes(), nil
}
