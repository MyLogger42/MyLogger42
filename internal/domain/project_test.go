package domain

import "testing"

func TestValidateSlug(t *testing.T) {
	good := []string{
		"ab",
		"auth-service",
		"a1",
		"x9-y9",
		// 63 chars — at the limit.
		"abcdefghijklmnopqrstuvwxyz0123456789-abcdefghijklmnopqrstuvwxy",
	}
	for _, s := range good {
		if err := ValidateSlug(s); err != nil {
			t.Errorf("ValidateSlug(%q) unexpected error: %v", s, err)
		}
	}
	bad := []string{
		"",      // empty
		"a",     // too short
		"1abc",  // starts with digit
		"-abc",  // starts with dash
		"Abc",   // uppercase
		"ab_cd", // underscore
		"ab cd", // space
		"ab/cd", // slash
		// 64 chars — over limit.
		"abcdefghijklmnopqrstuvwxyz0123456789-abcdefghijklmnopqrstuvwxyz0",
	}
	for _, s := range bad {
		if err := ValidateSlug(s); err == nil {
			t.Errorf("ValidateSlug(%q) expected error, got nil", s)
		}
	}
}
