package crypter

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// testKey is the default 32-byte (AES-256) key used across all tests and benchmarks.
// It is a random alphanumeric string as would be produced by e.g. pwgen -s 32.
// Tests that specifically cover different key lengths derive shorter keys by slicing it.
const testKey = "tp3gHlOSsRHlsEGuKIpu86sE1jM9KMZy"

func TestNew_InvalidKeyLength(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"",                      // 0 bytes
		"short",                 // 5 bytes
		strings.Repeat("k", 15), // one below AES-128
		strings.Repeat("k", 17), // one above AES-128
		strings.Repeat("k", 33), // one above AES-256
	} {
		_, err := New(key)
		if !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("key len %d: expected ErrInvalidKeyLength, got %v", len(key), err)
		}
	}
}

func TestNew_OK(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c == nil || c.aead == nil || c.nonceSize <= 0 {
		t.Fatalf("invalid crypter: %#v", c)
	}
}

func TestIsEncrypted_FastHeuristic(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
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

	c, err := New(testKey)
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

func TestEncrypt_Idempotent_RealCiphertext(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := c.Encrypt("hello")
	if err != nil {
		t.Fatalf("Encrypt err: %v", err)
	}
	enc2, err := c.Encrypt(enc)
	if err != nil {
		t.Fatalf("unexpected err on re-encrypt: %v", err)
	}
	if enc2 != enc {
		t.Fatalf("expected unchanged ciphertext, got %q", enc2)
	}
}

func TestEncrypt_SamePlaintext_DifferentCiphertext(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	enc1, err := c.Encrypt("hello")
	if err != nil {
		t.Fatalf("Encrypt 1 err: %v", err)
	}
	enc2, err := c.Encrypt("hello")
	if err != nil {
		t.Fatalf("Encrypt 2 err: %v", err)
	}
	if enc1 == enc2 {
		t.Fatal("expected different ciphertext for same plaintext (nonce must be random)")
	}
}

func TestDecrypt_Plaintext_FastReturn(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
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

	c, err := New(testKey)
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

	c, err := New(testKey)
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

	c, err := New(testKey)
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

func TestDecrypt_ShortCiphertext_NoAuthTag(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	overhead := c.aead.Overhead() // 16 for standard GCM

	cases := []struct {
		name   string
		rawLen int
	}{
		// Exactly nonceSize bytes: valid nonce, zero-length ct — no room for the GCM tag.
		{"nonce only", c.nonceSize},
		// nonceSize + overhead - 1: partial tag, still one byte short.
		{"partial tag", c.nonceSize + overhead - 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := base64.RawURLEncoding.EncodeToString(make([]byte, tc.rawLen))
			_, err := c.Decrypt(StartTag + payload + EndTag)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrInvalidCipherText) {
				t.Fatalf("expected ErrInvalidCipherText, got %v", err)
			}
		})
	}
}

func TestNew_KeySizes(t *testing.T) {
	t.Parallel()

	// Verify that all three valid AES key sizes (128/192/256) initialize and round-trip correctly.
	// Keys are derived by slicing testKey so the source of the bytes is always obvious.
	for _, size := range []int{16, 24, 32} {
		t.Run(fmt.Sprintf("AES-%d", size*8), func(t *testing.T) {
			t.Parallel()

			c, err := New(testKey[:size])
			if err != nil {
				t.Fatalf("unexpected err for %d-byte key: %v", size, err)
			}
			enc, err := c.Encrypt("hello")
			if err != nil {
				t.Fatalf("Encrypt err: %v", err)
			}
			out, err := c.Decrypt(enc)
			if err != nil {
				t.Fatalf("Decrypt err: %v", err)
			}
			if out != "hello" {
				t.Fatalf("roundtrip mismatch: got %q", out)
			}
		})
	}
}

func TestDecrypt_ShortRaw_InvalidCipherText(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	// Encode a payload that is valid base64 but decodes to fewer bytes than the nonce size (12).
	short := base64.RawURLEncoding.EncodeToString([]byte("tooshort")) // 8 bytes < 12
	_, err = c.Decrypt(StartTag + short + EndTag)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidCipherText) {
		t.Fatalf("expected ErrInvalidCipherText, got %v", err)
	}
}

func TestEncryptDecrypt_RoundTrip_Various(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
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
		// Multi-byte Unicode: verify byte-level round-trip is preserved.
		"こんにちは",
		"Привет мир",
		"مرحبا بالعالم",
		"🔐🗝️",
	}

	for _, in := range cases {
		t.Run(fmt.Sprintf("%q", in), func(t *testing.T) {
			t.Parallel()

			enc, err := c.Encrypt(in)
			if err != nil {
				t.Fatalf("Encrypt err: %v", err)
			}
			if enc == in {
				t.Fatalf("expected encrypted to differ")
			}
			if !strings.HasPrefix(enc, StartTag) || !strings.HasSuffix(enc, EndTag) {
				t.Fatalf("missing tags: %q", enc)
			}

			out, err := c.Decrypt(enc)
			if err != nil {
				t.Fatalf("Decrypt err: %v", err)
			}
			if out != in {
				t.Fatalf("roundtrip mismatch: got %q", out)
			}
		})
	}
}

func TestCrypter_ConcurrentEncryptDecrypt(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 64
	const iterations = 100

	errCh := make(chan error, goroutines)
	for i := range goroutines {
		go func() {
			in := fmt.Sprintf("secret-%d", i)
			for range iterations {
				enc, err := c.Encrypt(in)
				if err != nil {
					errCh <- fmt.Errorf("Encrypt: %w", err)
					return
				}
				out, err := c.Decrypt(enc)
				if err != nil {
					errCh <- fmt.Errorf("Decrypt: %w", err)
					return
				}
				if out != in {
					errCh <- fmt.Errorf("mismatch: got %q want %q", out, in)
					return
				}
			}
			errCh <- nil
		}()
	}

	for range goroutines {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
}

// TestEncrypt_AllocCount pins the number of heap allocations per Encrypt call.
// If this test fails after a code change, the change introduced an unexpected allocation.
// Expected: 5 allocs — owned nonce copy, []byte(data) conversion, raw buffer, output buffer, final string.
// The pool buffer used for random reading is not counted (comes from sync.Pool).
func TestEncrypt_AllocCount(t *testing.T) {
	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	const want = 5
	got := testing.AllocsPerRun(200, func() {
		_, _ = c.Encrypt("hello world")
	})
	if got != float64(want) {
		t.Fatalf("Encrypt allocs = %.0f, want %d", got, want)
	}
}

func TestDecrypt_AuthFail_Wrapped(t *testing.T) {
	t.Parallel()

	c1, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := New("Wr8mNqK2vXpL5cF0aY7dSeT4hJbGzUoQ")
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

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := c.Encrypt("hello world")
	if err != nil {
		t.Fatal(err)
	}

	// Decode the payload, flip one byte in the ciphertext body (after the nonce),
	// re-encode, and verify that GCM authentication rejects it.
	raw, err := base64.RawURLEncoding.DecodeString(enc[startLen : len(enc)-1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[c.nonceSize] ^= 0xFF // flip first ciphertext byte
	tampered := StartTag + base64.RawURLEncoding.EncodeToString(raw) + EndTag

	_, err = c.Decrypt(tampered)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

func TestDecrypt_TamperedAuthTag(t *testing.T) {
	t.Parallel()

	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}

	enc, err := c.Encrypt("hello world")
	if err != nil {
		t.Fatal(err)
	}

	// Flip the last byte of the GCM authentication tag.
	raw, err := base64.RawURLEncoding.DecodeString(enc[startLen : len(enc)-1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF // flip last byte of the auth tag
	tampered := StartTag + base64.RawURLEncoding.EncodeToString(raw) + EndTag

	_, err = c.Decrypt(tampered)
	if err == nil {
		t.Fatal("expected error for tampered auth tag")
	}
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

func BenchmarkIsEncrypted_Plaintext(b *testing.B) {
	c, err := New(testKey)
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
	c, err := New(testKey)
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
	c, err := New(testKey)
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

func BenchmarkEncrypt_Short_Parallel(b *testing.B) {
	c, err := New(testKey)
	if err != nil {
		b.Fatal(err)
	}
	in := "value"

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := c.Encrypt(in)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkEncrypt_100(b *testing.B) {
	c, err := New(testKey)
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

func BenchmarkEncrypt_100_Parallel(b *testing.B) {
	c, err := New(testKey)
	if err != nil {
		b.Fatal(err)
	}
	in := strings.Repeat("a", 100)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := c.Encrypt(in)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkDecrypt_PlaintextFastReturn(b *testing.B) {
	c, err := New(testKey)
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
	c, err := New(testKey)
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
	c, err := New(testKey)
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
