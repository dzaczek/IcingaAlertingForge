package auth

import (
	"crypto/subtle"

	"icinga-webhook-bridge/config"
)

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
	keyBytes := []byte(key)
	for k, r := range ks.routes {
		if subtle.ConstantTimeCompare(keyBytes, []byte(k)) == 1 {
			matched = r
			found = true
		}
	}
	return matched, found
}
