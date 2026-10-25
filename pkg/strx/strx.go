// Package strx holds the small string helpers the standard library lacks. cmp.Or, slices.Sorted(maps.Keys(m)) and
// strconv cover the rest.
package strx

import "strings"

// Truncate shortens s to at most n bytes without splitting a multi-byte character: a character cut by the limit is
// dropped whole.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// Deref returns *s, or "" for nil.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
