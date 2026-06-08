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

	t.Run("prune settings persist", func(t *testing.T) {
		body := `{"service_prune_after_days":30,"service_prune_dry_run":false}`
		req := httptest.NewRequest(http.MethodPatch, "/admin/settings", bytes.NewBufferString(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandlePatchSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		got := h.Store.Get()
		if got.ServicePruneAfterDays != 30 {
			t.Errorf("after_days not persisted: %d", got.ServicePruneAfterDays)
		}
		if got.ServicePruneDryRun == nil || *got.ServicePruneDryRun {
			t.Errorf("dry_run should persist as false, got %v", got.ServicePruneDryRun)
		}
	})
}
