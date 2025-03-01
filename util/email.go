package util

import "regexp"

// IsValidEmail checks if the given string is a valid email address.
//
// It returns true if the string is a valid email address, false otherwise.
//
// Note: This function does not check if the email address is already in use.
func IsValidEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	r := regexp.MustCompile(`^[\w-\.]+@([\w-]+\.)+[\w-]{2,4}$`)
	return r.MatchString(email)
}
