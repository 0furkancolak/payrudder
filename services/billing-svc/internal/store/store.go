// Package store holds billing-svc's SQL: the named queries in queries/*.sql and the Go that sqlc generates from them
// (sqlc/), plus the helpers that turn Go zero values into SQL NULLs.
package store

import "time"

// Ptr returns a pointer to v, for nullable query parameters.
func Ptr[T any](v T) *T { return &v }

// NilIfEmpty is NULL for the empty string.
func NilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Deref is the string a nullable column holds, "" for NULL.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// I32 narrows a small count (an attempt number, a page size, a calendar year) to the int32 a query parameter takes.
func I32(n int) int32 { return int32(n) } //nolint:gosec // attempt counters, page sizes and calendar years are small

// NilIfZero is NULL for the zero time.
func NilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
