package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	t.Run("sets headers and encodes body", func(t *testing.T) {
		rr := httptest.NewRecorder()
		WriteJSON(rr, http.StatusCreated, map[string]string{"key": "value"})

		if rr.Code != http.StatusCreated {
			t.Errorf("expected 201, got %d", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %q", ct)
		}
		if xct := rr.Header().Get("X-Content-Type-Options"); xct != "nosniff" {
			t.Errorf("expected nosniff, got %q", xct)
		}
		var got map[string]string
		if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if got["key"] != "value" {
			t.Errorf("expected key=value, got %q", got["key"])
		}
	})

	t.Run("200 status code", func(t *testing.T) {
		rr := httptest.NewRecorder()
		WriteJSON(rr, http.StatusOK, map[string]any{"ok": true})
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("nil data encodes as null", func(t *testing.T) {
		rr := httptest.NewRecorder()
		WriteJSON(rr, http.StatusOK, nil)
		body := rr.Body.String()
		if body != "null\n" {
			t.Errorf("unexpected body: %q", body)
		}
	})
}
