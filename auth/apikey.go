package auth

// KeyStore holds the mapping of API key values to their source identifiers.
type KeyStore struct {
	keys map[string]string // key_value -> source_name
}

// NewKeyStore creates a KeyStore from the provided key-to-source mapping.
func NewKeyStore(keys map[string]string) *KeyStore {
	return &KeyStore{keys: keys}
}

// ValidateKey checks if the given API key is valid.
// Returns the source name and true if the key is found, or empty string and false otherwise.
func (ks *KeyStore) ValidateKey(key string) (source string, ok bool) {
	if key == "" {
		return "", false
	}
	source, ok = ks.keys[key]
	return source, ok
}
