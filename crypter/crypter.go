package crypter

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const (
	// StartTag marks the beginning of the encrypted value.
	// Format: ENCv1[<base64url-raw(nonce||ciphertext||tag)>]
	StartTag = "ENCv1["

	// EndTag marks the end of the encrypted value.
	EndTag = "]"
)

var (
	// ErrInvalidCipherText is returned when the provided ciphertext is malformed,
	// not produced by this package, or authentication fails.
	ErrInvalidCipherText = errors.New("crypter: invalid ciphertext")

	// ErrInvalidKeyLength is returned when the provided key length is not valid for AES.
	// AES keys must be 16, 24, or 32 bytes.
	ErrInvalidKeyLength = errors.New("crypter: invalid key length")

	// ErrEmptyPayload is returned when the encrypted value has no payload between tags.
	ErrEmptyPayload = errors.New("crypter: empty payload")

	// ErrNewCipher is returned when aes.NewCipher fails.
	ErrNewCipher = errors.New("crypter: aes.NewCipher failed")

	// ErrNewGCM is returned when cipher.NewGCM fails.
	ErrNewGCM = errors.New("crypter: cipher.NewGCM failed")

	// ErrReadNonce is returned when reading cryptographic nonce from rand.Reader fails.
	ErrReadNonce = errors.New("crypter: read nonce failed")

	// ErrBase64Decode is returned when base64 decoding of the payload fails.
	ErrBase64Decode = errors.New("crypter: base64 decode failed")

	// ErrOpen is returned when AEAD authentication/decryption fails.
	ErrOpen = errors.New("crypter: aead open failed")
)

var startLen = len(StartTag)

// Crypter provides methods to encrypt and decrypt strings using AES-GCM.
// It uses a simple tagging format to identify encrypted values and avoid double encryption.
// The encrypted format is: ENCv1[<base64url-raw(nonce||ciphertext||tag)>]
// The nonce is generated randomly for each encryption and is included in the payload for decryption.
// The IsEncrypted method provides a fast heuristic to check if a string is encrypted by this package,
// without performing any cryptographic operations.
// The Encrypt method encrypts the input string if it is not already tagged, and returns the tagged encrypted string.
// The Decrypt method decrypts the input string if it is tagged, and returns the plaintext; otherwise it returns the input unchanged.
type Crypter struct {
	aead      cipher.AEAD
	nonceSize int
}

// New initializes a new Crypter with the provided secret key.
// The secret must be 16, 24, or 32 bytes (AES-128/192/256).
func New(secret string) (*Crypter, error) {
	key := []byte(secret)
	switch len(key) {
	case 16, 24, 32:
	default:
		return nil, ErrInvalidKeyLength
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNewCipher, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNewGCM, err)
	}

	return &Crypter{aead: aead, nonceSize: aead.NonceSize()}, nil
}

// IsEncrypted is the hot-path heuristic check. For maximum performance it only
// checks the opening tag.
//
// Contract: plaintext values will never start with StartTag.
func (c *Crypter) IsEncrypted(s string) bool {
	return len(s) > startLen && s[:startLen] == StartTag
}

// Encrypt returns data wrapped in ENCv1[...] using AES-GCM.
// If data is already tagged, it returns it unchanged.
func (c *Crypter) Encrypt(data string) (string, error) {
	if c.IsEncrypted(data) {
		return data, nil
	}

	nonce := make([]byte, c.nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("%w: %w", ErrReadNonce, err)
	}

	raw := c.aead.Seal(nonce, nonce, []byte(data), nil)
	return StartTag + base64.RawURLEncoding.EncodeToString(raw) + EndTag, nil
}

// Decrypt returns decrypted plaintext if data is tagged; otherwise it returns data unchanged.
func (c *Crypter) Decrypt(data string) (string, error) {
	if !c.IsEncrypted(data) {
		return data, nil
	}

	raw, err := unwrapAfterStartTag(data)
	if err != nil {
		return "", err
	}
	if len(raw) < c.nonceSize {
		return "", io.ErrUnexpectedEOF
	}

	nonce, ct := raw[:c.nonceSize], raw[c.nonceSize:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		// Map all auth/decrypt failures to a stable sentinel, but keep original for debugging via wrapping.
		return "", fmt.Errorf("%w: %w", ErrOpen, err)
	}
	return string(pt), nil
}

func unwrapAfterStartTag(s string) ([]byte, error) {
	// Caller already checked IsEncrypted.
	if len(s) <= startLen {
		return nil, ErrInvalidCipherText
	}
	if s[len(s)-1] != EndTag[0] {
		return nil, ErrInvalidCipherText
	}

	payload := s[startLen : len(s)-1] // slice only, no alloc
	if payload == "" {
		return nil, ErrEmptyPayload
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBase64Decode, err)
	}
	return raw, nil
}
