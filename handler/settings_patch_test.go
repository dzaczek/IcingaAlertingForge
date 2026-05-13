package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSettings_HandlePatchSettings_More(t *testing.T) {
	h := testSettingsHandler(t)

	t.Run("invalid JSON body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/admin/settings", bytes.NewBufferString("not json"))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandlePatchSettings(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid JSON, got %d", rr.Code)
		}
	})
}
