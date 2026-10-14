package models

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestServiceName(t *testing.T) {
	alert := func(name, summary string) GrafanaAlert {
		return GrafanaAlert{
			Labels:      map[string]string{"alertname": name},
			Annotations: map[string]string{"summary": summary},
		}
	}

	tests := []struct {
		name  string
		alert GrafanaAlert
		mode  string
		want  string
	}{
		{"default mode joins alertname and summary", alert("K8s-job-active", "zrh01-heidi:pbx-services-db-migrations"), ServiceNameModeAlertnameSummary, "K8s-job-active - zrh01-heidi:pbx-services-db-migrations"},
		{"empty mode behaves like default", alert("HighCPU", "web-01"), "", "HighCPU - web-01"},
		{"empty summary falls back to alertname", alert("HighCPU", ""), ServiceNameModeAlertnameSummary, "HighCPU"},
		{"whitespace-only summary falls back to alertname", alert("HighCPU", " \n\t "), ServiceNameModeAlertnameSummary, "HighCPU"},
		{"alertname mode ignores summary", alert("HighCPU", "web-01"), ServiceNameModeAlertname, "HighCPU"},
		{"bang is replaced", alert("Disk!", "web!01"), ServiceNameModeAlertnameSummary, "Disk_ - web_01"},
		{"newlines and repeated spaces collapse", alert("HighCPU", "web-01\n  is   hot\r\n"), ServiceNameModeAlertnameSummary, "HighCPU - web-01 is hot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.alert.ServiceName(tt.mode); got != tt.want {
				t.Errorf("ServiceName(%q) = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

func TestServiceName_TruncatesLongNamesUniquely(t *testing.T) {
	long := strings.Repeat("ż", 150) // 300 bytes of multi-byte runes
	a := GrafanaAlert{Labels: map[string]string{"alertname": "A"}, Annotations: map[string]string{"summary": long + "-1"}}
	b := GrafanaAlert{Labels: map[string]string{"alertname": "A"}, Annotations: map[string]string{"summary": long + "-2"}}

	na, nb := a.ServiceName(""), b.ServiceName("")
	if len(na) > maxServiceNameLen || len(nb) > maxServiceNameLen {
		t.Fatalf("names exceed %d bytes: %d, %d", maxServiceNameLen, len(na), len(nb))
	}
	if na == nb {
		t.Fatalf("distinct long summaries produced the same name %q", na)
	}
	if !utf8.ValidString(na) {
		t.Fatalf("truncated name is not valid UTF-8: %q", na)
	}
	if na != a.ServiceName("") {
		t.Fatal("truncation is not deterministic")
	}
}
