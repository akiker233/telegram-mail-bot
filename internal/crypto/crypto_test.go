package crypto

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("test-master-key")
	plaintext := "my-secret-app-password"

	encoded, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	decoded, err := Decrypt(key, encoded)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}

	if decoded != plaintext {
		t.Fatalf("round trip mismatch: got %q, want %q", decoded, plaintext)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	key := DeriveKey("correct-key")
	wrongKey := DeriveKey("wrong-key")

	encoded, err := Encrypt(key, "some-password")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	if _, err := Decrypt(wrongKey, encoded); err == nil {
		t.Fatal("expected error when decrypting with wrong key, got nil")
	}
}

func TestDecryptGarbageFails(t *testing.T) {
	key := DeriveKey("test-master-key")
	if _, err := Decrypt(key, "not-valid-base64-or-ciphertext"); err == nil {
		t.Fatal("expected error decrypting garbage input, got nil")
	}
}
