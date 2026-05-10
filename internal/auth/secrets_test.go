package auth

import "testing"

func TestGenerateSecretLengthAndCharset(t *testing.T) {
	s, err := GenerateSecret(32)
	if err != nil {
		t.Fatal(err)
	}
	// 32 bytes hex-encoded = 64 chars
	if len(s) != 64 {
		t.Fatalf("len=%d, want 64", len(s))
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("non-hex char %q", c)
		}
	}
}

func TestGenerateSecretUnique(t *testing.T) {
	a, _ := GenerateSecret(32)
	b, _ := GenerateSecret(32)
	if a == b {
		t.Fatal("two consecutive secrets collided — crypto/rand is broken or not used")
	}
}

func TestGenerateSecretRejectsSmallSizes(t *testing.T) {
	if _, err := GenerateSecret(8); err == nil {
		t.Fatal("expected error for nBytes < 16")
	}
}
