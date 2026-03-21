package auth

import "icinga-webhook-bridge/config"

// KeyStore holds the mapping of API key values to their source identifiers.
type KeyStore struct {
	routes map[string]config.WebhookRoute // key_value -> route
}

// NewKeyStore creates a KeyStore from the provided key-to-route mapping.
func NewKeyStore(routes map[string]config.WebhookRoute) *KeyStore {
	return &KeyStore{routes: routes}
}

// ValidateKey checks if the given API key is valid.
// Returns the resolved route and true if the key is found.
func (ks *KeyStore) ValidateKey(key string) (route config.WebhookRoute, ok bool) {
	if key == "" {
		return config.WebhookRoute{}, false
	}
	route, ok = ks.routes[key]
	return route, ok
}
