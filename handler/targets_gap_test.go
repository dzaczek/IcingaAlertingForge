package handler

import (
	"testing"

	"icinga-webhook-bridge/config"
)

func TestResolveScopedHosts(t *testing.T) {
	targets := map[string]config.TargetConfig{
		"a": {ID: "a", HostName: "alpha"},
		"b": {ID: "b", HostName: "beta"},
	}

	t.Run("specific host found", func(t *testing.T) {
		result, err := resolveScopedHosts(targets, "alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 1 || result[0].HostName != "alpha" {
			t.Errorf("expected [alpha], got %v", result)
		}
	})

	t.Run("specific host not found", func(t *testing.T) {
		_, err := resolveScopedHosts(targets, "nonexistent")
		if err == nil {
			t.Error("expected error for unknown host")
		}
	})

	t.Run("empty host returns all sorted", func(t *testing.T) {
		result, err := resolveScopedHosts(targets, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 results, got %d", len(result))
		}
		if result[0].HostName != "alpha" || result[1].HostName != "beta" {
			t.Errorf("expected sorted [alpha, beta], got %v", result)
		}
	})
}

func TestResolveSingleHost(t *testing.T) {
	targets := map[string]config.TargetConfig{
		"a": {ID: "a", HostName: "alpha"},
		"b": {ID: "b", HostName: "beta"},
	}

	t.Run("specific host found", func(t *testing.T) {
		result, err := resolveSingleHost(targets, "alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.HostName != "alpha" {
			t.Errorf("expected alpha, got %s", result.HostName)
		}
	})

	t.Run("specific host not found", func(t *testing.T) {
		_, err := resolveSingleHost(targets, "nonexistent")
		if err == nil {
			t.Error("expected error for unknown host")
		}
	})

	t.Run("multiple targets but no host specified", func(t *testing.T) {
		_, err := resolveSingleHost(targets, "")
		if err == nil {
			t.Error("expected error: host query parameter required for multiple targets")
		}
	})

	t.Run("single target no host ok", func(t *testing.T) {
		single := map[string]config.TargetConfig{
			"a": {ID: "a", HostName: "only"},
		}
		result, err := resolveSingleHost(single, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.HostName != "only" {
			t.Errorf("expected only, got %s", result.HostName)
		}
	})
}
