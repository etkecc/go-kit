package kit

import "sort"

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
