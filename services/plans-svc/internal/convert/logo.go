package convert

import "github.com/google/uuid"

func logoID(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
