package masterkey

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestMasterKey(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	mk, err := Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mk.Save("foo", keyFile); err != nil {
		t.Fatalf("mk.Save: %v", err)
	}

	got, err := Read("foo", keyFile)
	if err != nil {
		t.Fatalf("got.Read('foo'): %v", err)
	}
	if want := mk; !reflect.DeepEqual(want, got) {
		t.Errorf("Mismatch keys: %v != %v", want, got)
	}
	if _, err := Read("bar", keyFile); err == nil {
		t.Errorf("got.Read('bar') should have failed, but didn't")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	mk, err := Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	for i := 0; i < len(m); i++ {
		enc, err := mk.Encrypt(m[:i])
		if err != nil {
			t.Fatalf("mk.Encrypt: %v", err)
		}
		dec, err := mk.Decrypt(enc)
		if err != nil {
			t.Fatalf("mk.Decrypt: %v", err)
		}
		if !reflect.DeepEqual(m[:i], dec) {
			t.Errorf("Decrypted data doesn't match. Want %v, got %v", m[:i], dec)
		}
	}
}

func TestEncryptedKey(t *testing.T) {
	mk, err := Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ek, err := mk.NewEncryptedKey()
	if err != nil {
		t.Fatalf("mk.NewEncryptedKey: %v", err)
	}
	if _, err := mk.Decrypt(ek); err != nil {
		t.Fatalf("mk.Decrypt: %v", err)
	}
}
