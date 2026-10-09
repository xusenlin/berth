package config

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// keyringService is the service name of Berth's entries in the system
// keychain; each entry's account is a server ID, so the key reads
// berth/<server-id>.
const keyringService = "berth"

// Secrets stores passwords (and passphrases of private keys) outside
// config.json.
type Secrets interface {
	Get(serverID string) (string, error)
	Set(serverID, secret string) error
	Delete(serverID string) error
}

// Keyring is Secrets in the system keychain: Keychain on macOS, Credential
// Manager on Windows, Secret Service on Linux.
type Keyring struct{}

// Get returns the secret of a server, or "" when it has none.
func (Keyring) Get(serverID string) (string, error) {
	s, err := keyring.Get(keyringService, serverID)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return s, err
}

// Set stores the secret of a server; an empty secret deletes it.
func (k Keyring) Set(serverID, secret string) error {
	if secret == "" {
		return k.Delete(serverID)
	}
	return keyring.Set(keyringService, serverID, secret)
}

// Delete removes the secret of a server.
func (Keyring) Delete(serverID string) error {
	err := keyring.Delete(keyringService, serverID)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// MemorySecrets is Secrets in memory, for tests.
type MemorySecrets map[string]string

func (m MemorySecrets) Get(id string) (string, error) { return m[id], nil }
func (m MemorySecrets) Set(id, s string) error {
	if s == "" {
		delete(m, id)
	} else {
		m[id] = s
	}
	return nil
}
func (m MemorySecrets) Delete(id string) error { delete(m, id); return nil }
