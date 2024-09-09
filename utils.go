package kit

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Uniq removes duplicates from slice
func Uniq(slice []string) []string {
	uniq := map[string]struct{}{}
	for _, k := range slice {
		uniq[k] = struct{}{}
	}

	return MapKeys(uniq)
}

// MapKeys returns map keys only
func MapKeys[T string, V any](data map[string]V) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	return keys
}

// Unquote is a wrapper around strconv.Unquote, but returns original string if unquoting fails
func Unquote(s string) string {
	unquoted, err := strconv.Unquote(s)
	if err != nil {
		return s
	}
	return unquoted
}

// MergeMapKeys returns map keys only from multiple maps
func MergeMapKeys[V any](m map[string]V, adds ...map[string]V) []string {
	uniq := map[string]bool{}
	for k := range m {
		uniq[k] = true
	}
	for _, add := range adds {
		for k := range add {
			uniq[k] = true
		}
	}

	keys := make([]string, 0, len(uniq))
	for k := range uniq {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	return keys
}

// Chunk slices into chunks
func Chunk[T any](items []T, chunkSize int) (chunks [][]T) {
	chunks = make([][]T, 0, (len(items)/chunkSize)+1)
	for chunkSize < len(items) {
		items, chunks = items[chunkSize:], append(chunks, items[0:chunkSize:chunkSize])
	}
	return append(chunks, items)
}

// Truncate string
func Truncate(s string, length int) string {
	if s == "" {
		return s
	}

	wb := strings.Split(s, "")
	if length > len(wb) {
		length = len(wb)
	}

	out := strings.Join(wb[:length], "")
	if s == out {
		return s
	}
	return out + "..."
}

// Hash returns sha256 hash of a string
func Hash(str string) string {
	h := sha256.New()
	h.Write([]byte(str))
	return hex.EncodeToString(h.Sum(nil))
}
