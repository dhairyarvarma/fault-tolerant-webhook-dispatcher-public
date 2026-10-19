package crypto

import (
	"bytes"
	"testing"
)

func TestSecretBox_RoundTrip(t *testing.T) {
	box, err := NewSecretBox(bytes.Repeat([]byte("k"), keySize))
	if err != nil {
		t.Fatalf("NewSecretBox() error = %v", err)
	}

	ciphertext, err := box.Seal("super-secret-value")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Contains(ciphertext, []byte("super-secret-value")) {
		t.Fatal("ciphertext contains plaintext")
	}

	plaintext, err := box.Open(ciphertext)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if plaintext != "super-secret-value" {
		t.Errorf("plaintext = %q, want %q", plaintext, "super-secret-value")
	}
}

func TestSecretBox_EmptySecret(t *testing.T) {
	box, err := NewSecretBox(bytes.Repeat([]byte("k"), keySize))
	if err != nil {
		t.Fatalf("NewSecretBox() error = %v", err)
	}

	ciphertext, err := box.Seal("")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if ciphertext != nil {
		t.Errorf("ciphertext = %v, want nil for empty secret", ciphertext)
	}

	plaintext, err := box.Open(ciphertext)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if plaintext != "" {
		t.Errorf("plaintext = %q, want empty", plaintext)
	}
}

func TestNewSecretBox_InvalidKeySize(t *testing.T) {
	if _, err := NewSecretBox([]byte("too-short")); err == nil {
		t.Fatal("expected error for invalid key size")
	}
}
