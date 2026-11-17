// Package secrets encrypts small values, such as webhook bearer tokens,
// before they go into the database.
//
// The key lives in a file in the data directory and never in the database,
// so a copy of the database alone does not reveal the values. Values are
// sealed with AES-256-GCM and a random nonce, and carry a version prefix so
// the scheme can change later.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// KeyFile is the name of the key file in the data directory.
	KeyFile = "secrets.key"
	// KeySize is the length of the key in bytes. It selects AES-256.
	KeySize = 32

	// prefixV1 marks a value sealed with AES-256-GCM. The rest of the value
	// is the base64 of the nonce followed by the ciphertext and tag.
	prefixV1 = "v1:"
)

var (
	// ErrNoKey is returned when the key file does not exist.
	ErrNoKey = errors.New("secrets key file does not exist")
	// ErrBadKey is returned when the key file is not KeySize bytes.
	ErrBadKey = errors.New("secrets key file is not 32 bytes")
	// ErrDecrypt is returned when a value cannot be opened: it is
	// malformed, was changed, or was sealed with another key.
	ErrDecrypt = errors.New("cannot decrypt value: wrong key or damaged data")
)

// KeyPath returns the path of the key file in the data directory.
func KeyPath(dataPath string) string {
	return filepath.Join(dataPath, KeyFile)
}

// NewKey returns a new random key.
func NewKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secrets key: %w", err)
	}
	return key, nil
}

// LoadKey reads the key file at path. It returns an error wrapping ErrNoKey
// if the file does not exist.
func LoadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoKey, path)
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets key: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: %s", ErrBadKey, path)
	}
	return key, nil
}

// WriteKey writes key to a new file at path with mode 0600. It fails if the
// file already exists.
func WriteKey(path string, key []byte) error {
	if len(key) != KeySize {
		return ErrBadKey
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create secrets key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()       //nolint:errcheck
		os.Remove(path) //nolint:errcheck
		return fmt.Errorf("write secrets key: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path) //nolint:errcheck
		return fmt.Errorf("write secrets key: %w", err)
	}
	return nil
}

// LoadOrCreateKey reads the key file at path, or creates it with a new
// random key and mode 0600 if it does not exist. It reports whether it
// created the file.
func LoadOrCreateKey(path string) ([]byte, bool, error) {
	key, err := LoadKey(path)
	if err == nil {
		return key, false, nil
	}
	if !errors.Is(err, ErrNoKey) {
		return nil, false, err
	}
	key, err = NewKey()
	if err != nil {
		return nil, false, err
	}
	if err := WriteKey(path, key); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets cipher: %w", err)
	}
	return gcm, nil
}

// Encrypt seals plaintext with key and a random nonce. The result is text,
// safe to store in a database column.
func Encrypt(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return prefixV1 + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value made by Encrypt. It returns an error wrapping
// ErrDecrypt if the value is malformed, was changed, or was sealed with
// another key.
func Decrypt(key []byte, value string) ([]byte, error) {
	rest, ok := strings.CutPrefix(value, prefixV1)
	if !ok {
		return nil, fmt.Errorf("%w: unknown format", ErrDecrypt)
	}
	sealed, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return nil, fmt.Errorf("%w: bad encoding", ErrDecrypt)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, fmt.Errorf("%w: too short", ErrDecrypt)
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}
