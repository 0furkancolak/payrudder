// Package page holds the parts of keyset pagination every list endpoint shares: the size rule, the "read one more
// row" trim and the (created_at, id) cursor. A service maps a failure to its own error key; the rules live here once.
package page

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Size resolves the page size a client asked for. Zero means the default def; a negative size or one above max is not
// accepted (ok is false): the list endpoints answer InvalidArgument rather than quietly returning a different page than
// was asked for.
func Size(requested, def, max int32) (size int32, ok bool) {
	switch {
	case requested == 0:
		return def, true
	case requested < 0 || requested > max:
		return 0, false
	}
	return requested, true
}

// Trim cuts the extra row a keyset query reads (it asks for size+1) and says whether there is a next page.
func Trim[T any](rows []T, size int) (page []T, more bool) {
	if len(rows) > size {
		return rows[:size], true
	}
	return rows, false
}

// Cursor is the position after the last row of a page in a list ordered by created_at, then id, newest first. Its
// token is opaque to clients: "<created_at RFC 3339>|<id>".
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// Start is the cursor before the first page of a newest-first list: later than any row.
func Start() Cursor { return Cursor{At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ID: uuid.Max} }

// Token encodes the cursor.
func (c Cursor) Token() string { return c.At.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String() }

// ParseCursor decodes a token made by Cursor.Token; ok is false for anything else.
func ParseCursor(token string) (c Cursor, ok bool) {
	at, id, found := strings.Cut(token, "|")
	if !found {
		return Cursor{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return Cursor{}, false
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, false
	}
	return Cursor{At: t, ID: u}, true
}
