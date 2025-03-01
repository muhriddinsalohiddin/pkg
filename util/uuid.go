package util

import (
	"github.com/google/uuid"
)

// IsValidUUID checks if the given string is a valid UUID v4.
//
// It returns true if the string is a valid UUID, false otherwise.
//
// Note: This function does not check if the UUID is already in use.
func IsValidUUID(u string) bool {
	_, err := uuid.Parse(u)
	return err == nil
}
