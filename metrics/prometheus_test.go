package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestPrometheusCollector(t *testing.T) {
	appMetrics := NewCollector()
	pk := NewPerKeyCollector()

	// Record some data
	appMetrics.RecordRequest(100)
	appMetrics.RecordError()
	pk.Record("test-source", false)

	pc := NewPrometheusCollector(appMetrics, nil, nil, nil, nil, pk)
	reg := prometheus.NewRegistry()
	reg.MustRegister(pc)

	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	metricsOutput := string(body)

	expectedMetrics := []string{
		"iaf_uptime_seconds",
		"iaf_requests_total 1",
		"iaf_errors_total 1",
		"iaf_request_latency_milliseconds_bucket",
		"iaf_source_requests_total{source=\"test-source\"} 1",
		"iaf_source_last_seen_timestamp{source=\"test-source\"}",
	}

	for _, m := range expectedMetrics {
		if !strings.Contains(metricsOutput, m) {
			t.Errorf("Expected metric %q not found in output:\n%s", m, metricsOutput)
		}
	}
}
