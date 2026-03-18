package icinga

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIClient communicates with the Icinga2 REST API (port 5665)
// to submit passive check results.
type APIClient struct {
	BaseURL    string
	User       string
	Pass       string
	HTTPClient *http.Client
}

// NewAPIClient creates a new Icinga2 REST API client.
func NewAPIClient(baseURL, user, pass string, tlsSkipVerify bool) *APIClient {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: tlsSkipVerify},
	}
	return &APIClient{
		BaseURL: baseURL,
		User:    user,
		Pass:    pass,
		HTTPClient: &http.Client{
			Transport: transport,
			Timeout:   15 * time.Second,
		},
	}
}

// processCheckResultPayload is the JSON body sent to the Icinga2 process-check-result endpoint.
type processCheckResultPayload struct {
	Type       string `json:"type"`
	Filter     string `json:"filter"`
	ExitStatus int    `json:"exit_status"`
	PluginOutput string `json:"plugin_output"`
}

// SendCheckResult submits a passive check result to Icinga2 for the given host and service.
// exitStatus: 0=OK, 1=WARNING, 2=CRITICAL, 3=UNKNOWN
func (c *APIClient) SendCheckResult(host, service string, exitStatus int, message string) error {
	payload := processCheckResultPayload{
		Type:         "Service",
		Filter:       fmt.Sprintf(`host.name==%q && service.name==%q`, host, service),
		ExitStatus:   exitStatus,
		PluginOutput: message,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("icinga api: marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/v1/actions/process-check-result", c.BaseURL)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("icinga api: create request: %w", err)
	}

	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("icinga api: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// CreateService creates a dummy passive service in Icinga2 via the REST API.
// Uses PUT /v1/objects/services/<host>!<service> to create the object directly.
// Labels and annotations from the Grafana webhook are stored as Icinga2 attributes
// so operators can see the full alert context in the Icinga2 UI.
func (c *APIClient) CreateService(host, name string, labels, annotations map[string]string) error {
	// Build notes from annotations (summary + description)
	notes := "Managed by webhook-bridge | auto-created"
	if s := annotations["summary"]; s != "" {
		notes = s
	}
	if d := annotations["description"]; d != "" {
		notes += "\n" + d
	}

	// Store all labels and annotations as custom vars for full context
	vars := map[string]any{}
	for k, v := range labels {
		vars["grafana_label_"+k] = v
	}
	for k, v := range annotations {
		vars["grafana_annotation_"+k] = v
	}

	serviceAttrs := map[string]any{
		"check_command":         "dummy",
		"enable_active_checks":  false,
		"enable_passive_checks": true,
		"check_interval":        300,
		"max_check_attempts":    1,
		"notes":                 notes,
		"vars":                  vars,
	}

	// Set display_name if summary is available for better readability in UI
	if s := annotations["summary"]; s != "" {
		serviceAttrs["display_name"] = name + " - " + s
	}

	// Set notes_url if runbook_url or dashboard_url is provided
	if u := annotations["runbook_url"]; u != "" {
		serviceAttrs["notes_url"] = u
	} else if u := annotations["dashboard_url"]; u != "" {
		serviceAttrs["notes_url"] = u
	}

	// Set action_url if panel_url is provided
	if u := annotations["panel_url"]; u != "" {
		serviceAttrs["action_url"] = u
	}

	attrs := map[string]any{
		"attrs":     serviceAttrs,
		"templates": []string{"generic-service"},
	}

	body, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("icinga api: marshal create payload: %w", err)
	}

	url := fmt.Sprintf("%s/v1/objects/services/%s!%s", c.BaseURL, host, name)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("icinga api: create request: %w", err)
	}

	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("icinga api: send create request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("icinga api: create service %q: status %d: %s", name, resp.StatusCode, string(respBody))
	}

	return nil
}

// DeleteService removes a service from Icinga2 via the REST API.
// Uses DELETE /v1/objects/services/<host>!<service> with cascade=true.
func (c *APIClient) DeleteService(host, name string) error {
	url := fmt.Sprintf("%s/v1/objects/services/%s!%s?cascade=1", c.BaseURL, host, name)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("icinga api: create delete request: %w", err)
	}

	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("icinga api: send delete request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("icinga api: delete service %q: status %d: %s", name, resp.StatusCode, string(respBody))
	}

	return nil
}

// GetServiceStatus queries Icinga2 for the current status of a service on a host.
func (c *APIClient) GetServiceStatus(host, service string) (exitStatus int, output string, checkTime time.Time, err error) {
	url := fmt.Sprintf("%s/v1/objects/services?filter=host.name==%q&&service.name==%q&attrs=last_check_result",
		c.BaseURL, host, service)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: create request: %w", err)
	}
	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, "", time.Time{}, fmt.Errorf("icinga api: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []struct {
			Attrs struct {
				LastCheckResult struct {
					ExitStatus   float64 `json:"exit_status"`
					Output       string  `json:"output"`
					ExecutionEnd float64 `json:"execution_end"`
				} `json:"last_check_result"`
			} `json:"attrs"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: decode response: %w", err)
	}

	if len(result.Results) == 0 {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: service %q not found on host %q", service, host)
	}

	r := result.Results[0].Attrs.LastCheckResult
	ts := time.Unix(int64(r.ExecutionEnd), 0)
	return int(r.ExitStatus), r.Output, ts, nil
}
