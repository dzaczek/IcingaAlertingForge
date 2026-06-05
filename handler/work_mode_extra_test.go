package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"icinga-webhook-bridge/models"
)

func TestHandleWorkMode_Coverage(t *testing.T) {
	mockIcinga := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":[{"code":200}]}`))
	}

	h := testWebhookHandler(t, mockIcinga)

	tests := []struct {
		name    string
		status  string
		summary string
	}{
		{
			name:    "resolved with summary",
			status:  "resolved",
			summary: "System recovered",
		},
		{
			name:    "resolved without summary",
			status:  "resolved",
			summary: "",
		},
		{
			name:    "firing with summary",
			status:  "firing",
			summary: "High CPU",
		},
		{
			name:    "firing without summary",
			status:  "firing",
			summary: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := models.GrafanaPayload{
				Status: tt.status,
				Alerts: []models.GrafanaAlert{
					{
						Status: tt.status,
						Labels: map[string]string{
							"alertname": "TestAlert",
						},
						Annotations: map[string]string{
							"summary": tt.summary,
						},
					},
				},
			}

			body, _ := json.Marshal(payload)
			req := httptest.NewRequest("POST", "/webhook", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer valid-key")

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected 200 OK, got %d", rr.Code)
			}
		})
	}
}
