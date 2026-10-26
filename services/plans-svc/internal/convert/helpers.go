package convert

import (
	"github.com/google/uuid"
)

// Str is the string p points to, empty for nil.
func Str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// UUIDStr is the text of id, empty for nil.
func UUIDStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
