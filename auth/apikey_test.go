package auth

import "testing"

func TestValidateKey_ValidKey(t *testing.T) {
	ks := NewKeyStore(map[string]string{
		"key-prod": "grafana-prod",
		"key-dev":  "grafana-dev",
	})

	source, ok := ks.ValidateKey("key-prod")
	if !ok {
		t.Error("expected valid key to return ok=true")
	}
	if source != "grafana-prod" {
		t.Errorf("expected source grafana-prod, got %s", source)
	}
}

func TestValidateKey_InvalidKey(t *testing.T) {
	ks := NewKeyStore(map[string]string{
		"key-prod": "grafana-prod",
	})

	_, ok := ks.ValidateKey("wrong-key")
	if ok {
		t.Error("expected invalid key to return ok=false")
	}
}

func TestValidateKey_EmptyKey(t *testing.T) {
	ks := NewKeyStore(map[string]string{
		"key-prod": "grafana-prod",
	})

	_, ok := ks.ValidateKey("")
	if ok {
		t.Error("expected empty key to return ok=false")
	}
}

func TestValidateKey_MultipleKeys(t *testing.T) {
	ks := NewKeyStore(map[string]string{
		"key-prod":    "grafana-prod",
		"key-dev":     "grafana-dev",
		"key-staging": "grafana-staging",
	})

	tests := []struct {
		key      string
		wantSrc  string
		wantOK   bool
	}{
		{"key-prod", "grafana-prod", true},
		{"key-dev", "grafana-dev", true},
		{"key-staging", "grafana-staging", true},
		{"key-unknown", "", false},
	}

	for _, tt := range tests {
		source, ok := ks.ValidateKey(tt.key)
		if ok != tt.wantOK {
			t.Errorf("ValidateKey(%q): ok=%v, want %v", tt.key, ok, tt.wantOK)
		}
		if source != tt.wantSrc {
			t.Errorf("ValidateKey(%q): source=%q, want %q", tt.key, source, tt.wantSrc)
		}
	}
}
