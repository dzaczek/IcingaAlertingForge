package auth

import (
	"crypto/sha256"
	"crypto/subtle"

	"icinga-webhook-bridge/config"
)

// SecureCompare performs a constant-time comparison of two strings
// by hashing them first, preventing length leakage.
func SecureCompare(a, b string) bool {
	hashA := sha256.Sum256([]byte(a))
	hashB := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(hashA[:], hashB[:]) == 1
}

// KeyStore holds the mapping of API key values to their source identifiers.
type KeyStore struct {
	routes map[string]config.WebhookRoute // key_value -> route
}

// NewKeyStore creates a KeyStore from the provided key-to-route mapping.
func NewKeyStore(routes map[string]config.WebhookRoute) *KeyStore {
	return &KeyStore{routes: routes}
}

// ValidateKey checks if the given API key is valid.
// Uses constant-time comparison to prevent timing attacks.
func (ks *KeyStore) ValidateKey(key string) (route config.WebhookRoute, ok bool) {
	if key == "" {
		return config.WebhookRoute{}, false
	}

	var matched config.WebhookRoute
	found := false
	for k, r := range ks.routes {
		if SecureCompare(key, k) {
			matched = r
			found = true
		}
	}
	return matched, found
}
