package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"

	"icinga-webhook-bridge/config"
)

var secureCompareKey []byte

func init() {
	secureCompareKey = make([]byte, 32)
	if _, err := rand.Read(secureCompareKey); err != nil {
		panic("failed to generate secure compare key: " + err.Error())
	}
}

// SecureCompare performs a constant-time comparison of two strings
// by hashing them first with HMAC, preventing length leakage.
func SecureCompare(a, b string) bool {
	macA := hmac.New(sha256.New, secureCompareKey)
	macA.Write([]byte(a))
	hashA := macA.Sum(nil)

	macB := hmac.New(sha256.New, secureCompareKey)
	macB.Write([]byte(b))
	hashB := macB.Sum(nil)

	return hmac.Equal(hashA, hashB)
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
