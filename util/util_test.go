package util

import (
	"testing"
)

func TestEmail(t *testing.T) {
	var tests = []struct {
		arg      string
		expected bool
	}{
		{"john.doe@gamil.com", true},
		{"john.doe@gmail", false},
	}

	for _, test := range tests {
		got := IsValidEmail(test.arg)
		if got != test.expected {
			t.Error("For email value ", test.arg, " Expected ", test.expected, " Got ", false)
		}
	}
}

func TestLogin(t *testing.T) {
	var tests = []struct {
		arg      string
		expected bool
	}{
		{"john_doe", true},
		{"ab", false},
		{"13", false},
	}

	for _, test := range tests {
		got := IsValidLogin(test.arg)
		if got != test.expected {
			t.Error("For login value ", test.arg, " Expected ", test.expected, " Got ", false)
		}
	}
}

func TestPhone(t *testing.T) {
	var tests = []struct {
		arg      string
		expected bool
	}{
		{"+998901234567", true},
		{"+998991234567", true},
		{"+998901234567", true},
		{"+99890123456788", false},
		{"+9989012345678888", false},
		{"+998901234567888888", false},
		{"+99890123456788888888", false},
		{"+9989012345678888888888", false},
		{"+998937", false},
	}

	for _, test := range tests {
		got := IsValidPhone(test.arg)
		if got != test.expected {
			t.Error("For phone value ", test.arg, " Expected ", test.expected, " Got ", got)
		}
	}

}

func TestUUID(t *testing.T) {
	var tests = []struct {
		arg      string
		expected bool
	}{
		{"A987FBC9-4BED-3078-CF07-9141BA07C9F3", true},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0", true},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0a0a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0a0a0a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0a0a0a0a0a0a0a0a0", false},
		{"a0a0a0a0-a0a0-a0a", false},
	}

	for _, test := range tests {
		got := IsValidUUID(test.arg)
		if got != test.expected {
			t.Error("For UUID value ", test.arg, " Expected ", test.expected, " Got ", got)
		}
	}
}
