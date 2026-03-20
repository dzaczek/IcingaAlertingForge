package icinga

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

	reqURL := fmt.Sprintf("%s/v1/actions/process-check-result", c.BaseURL)
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(body))
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

// HostInfo holds information about a host in Icinga2.
type HostInfo struct {
	Exists       bool
	CheckCommand string
	DisplayName  string
	ManagedBy    string // value of vars.managed_by
	Address      string
}

// IsManagedByUs returns true if the host was created by webhook-bridge.
func (h HostInfo) IsManagedByUs() bool {
	return h.ManagedBy == "webhook-bridge"
}

// IsDummy returns true if the host uses the "dummy" check command.
func (h HostInfo) IsDummy() bool {
	return h.CheckCommand == "dummy"
}

// GetHostInfo retrieves detailed host information from Icinga2.
func (c *APIClient) GetHostInfo(host string) (HostInfo, error) {
	reqURL := fmt.Sprintf("%s/v1/objects/hosts/%s", c.BaseURL, url.PathEscape(host))
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return HostInfo{}, fmt.Errorf("icinga api: create request: %w", err)
	}
	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return HostInfo{}, fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return HostInfo{Exists: false}, nil
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return HostInfo{}, fmt.Errorf("icinga api: check host %q: status %d: %s", host, resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []struct {
			Attrs struct {
				CheckCommand string         `json:"check_command"`
				DisplayName  string         `json:"display_name"`
				Address      string         `json:"address"`
				Vars         map[string]any `json:"vars"`
			} `json:"attrs"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return HostInfo{}, fmt.Errorf("icinga api: decode host response: %w", err)
	}

	if len(result.Results) == 0 {
		return HostInfo{Exists: false}, nil
	}

	attrs := result.Results[0].Attrs
	info := HostInfo{
		Exists:       true,
		CheckCommand: attrs.CheckCommand,
		DisplayName:  attrs.DisplayName,
		Address:      attrs.Address,
	}

	if mb, ok := attrs.Vars["managed_by"].(string); ok {
		info.ManagedBy = mb
	}

	return info, nil
}

// HostExists is a convenience wrapper around GetHostInfo.
func (c *APIClient) HostExists(host string) (bool, error) {
	info, err := c.GetHostInfo(host)
	if err != nil {
		return false, err
	}
	return info.Exists, nil
}

// CreateHost creates a dummy host in Icinga2 via the REST API.
// The host is marked with vars.managed_by = "webhook-bridge" so we can
// detect it on subsequent startups and avoid conflicts with Director.
func (c *APIClient) CreateHost(name, displayName, address string) error {
	if address == "" {
		address = "127.0.0.1"
	}
	if displayName == "" {
		displayName = name + " (webhook-bridge)"
	}

	attrs := map[string]any{
		"attrs": map[string]any{
			"check_command":        "dummy",
			"enable_active_checks": false,
			"address":              address,
			"display_name":         displayName,
			"vars": map[string]any{
				"managed_by": "webhook-bridge",
				"os":         "Linux",
			},
		},
		"templates": []string{"generic-host"},
	}

	body, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("icinga api: marshal host payload: %w", err)
	}

	reqURL := fmt.Sprintf("%s/v1/objects/hosts/%s", c.BaseURL, url.PathEscape(name))
	req, err := http.NewRequest(http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("icinga api: create request: %w", err)
	}

	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("icinga api: send create host request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("icinga api: create host %q: status %d: %s", name, resp.StatusCode, string(respBody))
	}

	return nil
}

// ListServices returns all services for the given host from Icinga2.
func (c *APIClient) ListServices(host string) ([]ServiceInfo, error) {
	filter := map[string]any{
		"filter": fmt.Sprintf(`host.name==%q`, host),
		"attrs":  []string{"name", "display_name", "last_check_result", "notes", "vars"},
	}

	body, err := json.Marshal(filter)
	if err != nil {
		return nil, fmt.Errorf("icinga api: marshal filter: %w", err)
	}

	reqURL := fmt.Sprintf("%s/v1/objects/services", c.BaseURL)
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("icinga api: create request: %w", err)
	}
	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-HTTP-Method-Override", "GET")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("icinga api: list services: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []struct {
			Name  string `json:"name"`
			Attrs struct {
				Name        string  `json:"name"`
				DisplayName string  `json:"display_name"`
				Notes       string  `json:"notes"`
				Vars        map[string]any `json:"vars"`
				LastCheckResult *struct {
					ExitStatus   float64 `json:"exit_status"`
					Output       string  `json:"output"`
					ExecutionEnd float64 `json:"execution_end"`
				} `json:"last_check_result"`
			} `json:"attrs"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("icinga api: decode response: %w", err)
	}

	var services []ServiceInfo
	for _, r := range result.Results {
		svc := ServiceInfo{
			Name:        r.Attrs.Name,
			DisplayName: r.Attrs.DisplayName,
			Notes:       r.Attrs.Notes,
		}
		if r.Attrs.LastCheckResult != nil {
			svc.ExitStatus = int(r.Attrs.LastCheckResult.ExitStatus)
			svc.Output = r.Attrs.LastCheckResult.Output
			svc.LastCheck = time.Unix(int64(r.Attrs.LastCheckResult.ExecutionEnd), 0)
			svc.HasCheckResult = true
		}
		services = append(services, svc)
	}

	return services, nil
}

// ServiceInfo holds basic service information from Icinga2.
type ServiceInfo struct {
	Name           string    `json:"name"`
	DisplayName    string    `json:"display_name"`
	Notes          string    `json:"notes"`
	ExitStatus     int       `json:"exit_status"`
	Output         string    `json:"output"`
	LastCheck      time.Time `json:"last_check"`
	HasCheckResult bool      `json:"has_check_result"`
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

	reqURL := fmt.Sprintf("%s/v1/objects/services/%s!%s", c.BaseURL, url.PathEscape(host), url.PathEscape(name))
	req, err := http.NewRequest(http.MethodPut, reqURL, bytes.NewReader(body))
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
	reqURL := fmt.Sprintf("%s/v1/objects/services/%s!%s?cascade=1", c.BaseURL, url.PathEscape(host), url.PathEscape(name))
	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
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
	reqURL := fmt.Sprintf("%s/v1/objects/services?filter=host.name==%q&&service.name==%q&attrs=last_check_result",
		c.BaseURL, host, service)

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
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
