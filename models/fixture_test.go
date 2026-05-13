package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseGrafanaFixtures(t *testing.T) {
	entries, err := filepath.Glob("../testdata/webhooks/grafana/**/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no fixture files found")
	}

	for _, path := range entries {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var payload GrafanaPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("failed to parse %s: %v", path, err)
			}
			if payload.Status == "" {
				t.Errorf("expected non-empty status in %s", path)
			}
			// Each alert should have a name
			for _, a := range payload.Alerts {
				if a.AlertName() == "" {
					t.Errorf("alert missing alertname in %s", path)
				}
			}
		})
	}
}

func TestParseAlertmanagerFixtures(t *testing.T) {
	entries, err := filepath.Glob("../testdata/webhooks/alertmanager/**/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no fixture files found")
	}

	for _, path := range entries {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var payload AlertmanagerPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("failed to parse %s: %v", path, err)
			}
			// Version should be set
			if payload.Version == "" {
				t.Errorf("expected non-empty version in %s", path)
			}
		})
	}
}

func FuzzWebhookParse(f *testing.F) {
	// Seed corpus from fixtures
	seeds, _ := filepath.Glob("../testdata/webhooks/**/*.json")
	for _, s := range seeds {
		data, err := os.ReadFile(s)
		if err != nil {
			continue
		}
		f.Add(data)
	}
	// Add edge cases
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"status":"firing","alerts":null}`))
	f.Add([]byte(`{"status":"","alerts":[]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Test Grafana parser
		var gp GrafanaPayload
		if err := json.Unmarshal(data, &gp); err != nil {
			return // invalid JSON is expected and should not panic
		}
		// Verify invariants
		for _, a := range gp.Alerts {
			_ = a.AlertName()
			_ = a.Severity()
			_ = a.Mode()
			_ = a.TestAction()
			_ = a.Summary()
		}

		// Test Alertmanager parser
		var ap AlertmanagerPayload
		if err := json.Unmarshal(data, &ap); err != nil {
			return
		}
		_ = len(ap.Alerts)
	})
}
