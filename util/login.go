package util

import (
	"regexp"
)

// IsValidLogin checks if the given string is a valid login.
//
// It returns true if the string is a valid login, false otherwise.
//
// Note: This function does not check if the login is already in use.
func IsValidLogin(login string) bool {
	if len(login) < 3 {
		return false
	}
	r := regexp.MustCompile("^[a-zA-Z0-9_]+$")
	return r.MatchString(login)
}
