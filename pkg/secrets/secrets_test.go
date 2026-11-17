package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"", "token", "bkua_0123456789abcdef", "ünïcødé\n"} {
		enc, err := Encrypt(key, []byte(plain))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(enc, "v1:") {
			t.Errorf("Encrypt(%q) = %q, want a v1: prefix", plain, enc)
		}
		if plain != "" && strings.Contains(enc, plain) {
			t.Errorf("Encrypt(%q) = %q holds the plaintext", plain, enc)
		}
		got, err := Decrypt(key, enc)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", enc, err)
		}
		if string(got) != plain {
			t.Errorf("round trip = %q, want %q", got, plain)
		}
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	key, _ := NewKey()
	a, _ := Encrypt(key, []byte("same"))
	b, _ := Encrypt(key, []byte("same"))
	if a == b {
		t.Error("two encryptions of the same value are equal")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key, _ := NewKey()
	other, _ := NewKey()
	enc, err := Encrypt(key, []byte("token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(other, enc); !errors.Is(err, ErrDecrypt) {
		t.Errorf("Decrypt with the wrong key = %v, want ErrDecrypt", err)
	}
}

func TestDecryptTampered(t *testing.T) {
	key, _ := NewKey()
	enc, err := Encrypt(key, []byte("token"))
	if err != nil {
		t.Fatal(err)
	}

	// Flip one bit in each byte of the nonce, ciphertext and tag in turn.
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, "v1:"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		bad := bytes.Clone(raw)
		bad[i] ^= 0x01
		if _, err := Decrypt(key, "v1:"+base64.StdEncoding.EncodeToString(bad)); !errors.Is(err, ErrDecrypt) {
			t.Fatalf("Decrypt of a value changed at byte %d = %v, want ErrDecrypt", i, err)
		}
	}

	for _, bad := range []string{"", "token", "v1:", "v1:!!!", "v1:AAAA", "v2:" + enc[3:]} {
		if _, err := Decrypt(key, bad); !errors.Is(err, ErrDecrypt) {
			t.Errorf("Decrypt(%q) = %v, want ErrDecrypt", bad, err)
		}
	}
}

func TestLoadKeyMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), KeyFile)
	if _, err := LoadKey(path); !errors.Is(err, ErrNoKey) {
		t.Errorf("LoadKey on a missing file = %v, want ErrNoKey", err)
	}
}

func TestLoadKeyWrongSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), KeyFile)
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(path); !errors.Is(err, ErrBadKey) {
		t.Errorf("LoadKey on a short file = %v, want ErrBadKey", err)
	}
	if _, _, err := LoadOrCreateKey(path); !errors.Is(err, ErrBadKey) {
		t.Errorf("LoadOrCreateKey on a short file = %v, want ErrBadKey", err)
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	path := KeyPath(t.TempDir())
	key, created, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created || len(key) != KeySize {
		t.Fatalf("LoadOrCreateKey = %d bytes, created %v", len(key), created)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if mode := fi.Mode().Perm(); mode != 0o600 {
			t.Errorf("key file mode = %o, want 600", mode)
		}
	}

	again, created, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if created || !bytes.Equal(again, key) {
		t.Error("LoadOrCreateKey replaced an existing key")
	}

	if err := WriteKey(path, key); err == nil {
		t.Error("WriteKey overwrote an existing key file")
	}
}
