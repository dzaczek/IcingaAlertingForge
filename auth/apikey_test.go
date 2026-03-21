package auth

import (
	"testing"

	"icinga-webhook-bridge/config"
)

func TestValidateKey_ValidKey(t *testing.T) {
	ks := NewKeyStore(map[string]config.WebhookRoute{
		"key-prod": {Source: "grafana-prod", TargetID: "team-a"},
		"key-dev":  {Source: "grafana-dev", TargetID: "team-b"},
	})

	route, ok := ks.ValidateKey("key-prod")
	if !ok {
		t.Error("expected valid key to return ok=true")
	}
	if route.Source != "grafana-prod" {
		t.Errorf("expected source grafana-prod, got %s", route.Source)
	}
	if route.TargetID != "team-a" {
		t.Errorf("expected target team-a, got %s", route.TargetID)
	}
}

func TestValidateKey_InvalidKey(t *testing.T) {
	ks := NewKeyStore(map[string]config.WebhookRoute{
		"key-prod": {Source: "grafana-prod", TargetID: "team-a"},
	})

	_, ok := ks.ValidateKey("wrong-key")
	if ok {
		t.Error("expected invalid key to return ok=false")
	}
}

func TestValidateKey_EmptyKey(t *testing.T) {
	ks := NewKeyStore(map[string]config.WebhookRoute{
		"key-prod": {Source: "grafana-prod", TargetID: "team-a"},
	})

	_, ok := ks.ValidateKey("")
	if ok {
		t.Error("expected empty key to return ok=false")
	}
}

func TestValidateKey_MultipleKeys(t *testing.T) {
	ks := NewKeyStore(map[string]config.WebhookRoute{
		"key-prod":    {Source: "grafana-prod", TargetID: "team-a"},
		"key-dev":     {Source: "grafana-dev", TargetID: "team-b"},
		"key-staging": {Source: "grafana-staging", TargetID: "team-c"},
	})

	tests := []struct {
		key        string
		wantSrc    string
		wantTarget string
		wantOK     bool
	}{
		{"key-prod", "grafana-prod", "team-a", true},
		{"key-dev", "grafana-dev", "team-b", true},
		{"key-staging", "grafana-staging", "team-c", true},
		{"key-unknown", "", "", false},
	}

	for _, tt := range tests {
		route, ok := ks.ValidateKey(tt.key)
		if ok != tt.wantOK {
			t.Errorf("ValidateKey(%q): ok=%v, want %v", tt.key, ok, tt.wantOK)
		}
		if route.Source != tt.wantSrc {
			t.Errorf("ValidateKey(%q): source=%q, want %q", tt.key, route.Source, tt.wantSrc)
		}
		if route.TargetID != tt.wantTarget {
			t.Errorf("ValidateKey(%q): target=%q, want %q", tt.key, route.TargetID, tt.wantTarget)
		}
	}
}
