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

// NilIfZero is NULL for the zero time.
func NilIfZero(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
