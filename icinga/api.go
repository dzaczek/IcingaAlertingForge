package icinga

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ConflictPolicy defines how to handle existing objects not managed by the bridge.
type ConflictPolicy string

const (
	ConflictPolicySkip ConflictPolicy = "skip" // Skip the operation and log a warning
	ConflictPolicyWarn ConflictPolicy = "warn" // Proceed with the operation but log a warning
	ConflictPolicyFail ConflictPolicy = "fail" // Fail the operation and return an error
)

// ErrConflict is returned when an operation is refused due to a conflict policy.
type ErrConflict struct {
	Message string
}

func (e *ErrConflict) Error() string {
	return e.Message
}

// APIClient communicates with the Icinga2 REST API (port 5665)
// to submit passive check results.
type APIClient struct {
	mu             sync.RWMutex
	BaseURL        string
	User           string
	Pass           string
	HTTPClient     *http.Client
	Debug          *DebugRing // optional: captures request/response pairs for dev panel
	ConflictPolicy ConflictPolicy
	Force          bool
}

const (
	ManagedByIAF    = "IcingaAlertingForge"
	ManagedByLegacy = "webhook-bridge"
)

func isManagedByIAF(managedBy string) bool {
	return managedBy == ManagedByIAF
}

func isManagedByLegacy(managedBy string) bool {
	return managedBy == ManagedByLegacy
}

func isManagedByUs(managedBy string) bool {
	return isManagedByIAF(managedBy) || isManagedByLegacy(managedBy)
}

// NewAPIClient creates a new Icinga2 REST API client.
func NewAPIClient(baseURL, user, pass string, tlsSkipVerify bool) *APIClient {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: tlsSkipVerify},
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

// UpdateCredentials replaces the API client's connection details for hot-reload.
func (c *APIClient) UpdateCredentials(baseURL, user, pass string, tlsSkipVerify bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.BaseURL = baseURL
	c.User = user
	c.Pass = pass
	c.HTTPClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = tlsSkipVerify
}

// recordDebug stores an API interaction in the debug ring buffer if present.
func (c *APIClient) recordDebug(method, url string, reqBody []byte, statusCode int, respBody []byte, dur time.Duration, reqErr error) {
	if c.Debug == nil {
		return
	}
	entry := DebugEntry{
		Timestamp:    time.Now(),
		Direction:    "outbound",
		Method:       method,
		URL:          url,
		RequestBody:  string(reqBody),
		StatusCode:   statusCode,
		ResponseBody: string(respBody),
		DurationMs:   dur.Milliseconds(),
	}
	if reqErr != nil {
		entry.Error = reqErr.Error()
	}
	c.Debug.Push(entry)
}

// processCheckResultPayload is the JSON body sent to the Icinga2 process-check-result endpoint.
type processCheckResultPayload struct {
	Type         string `json:"type"`
	Filter       string `json:"filter"`
	ExitStatus   int    `json:"exit_status"`
	PluginOutput string `json:"plugin_output"`
}

// SendCheckResult submits a passive check result to Icinga2 for the given host and service.
// exitStatus: 0=OK, 1=WARNING, 2=CRITICAL, 3=UNKNOWN
func (c *APIClient) SendCheckResult(host, service string, exitStatus int, message string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
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

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(req.Method, reqURL, body, 0, nil, time.Since(start), err)
		return fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(req.Method, reqURL, body, resp.StatusCode, respBody, time.Since(start), nil)

	if resp.StatusCode != http.StatusOK {
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

// HostNotificationConfig holds host-level mail notification customization.
type HostNotificationConfig struct {
	Users         []string
	Groups        []string
	ServiceStates []string
	HostStates    []string
}

// HostSpec describes a managed dummy host to create in Icinga2.
type HostSpec struct {
	Name         string
	DisplayName  string
	Address      string
	Notification HostNotificationConfig
}

// IsManagedByUs returns true if the host is managed by IAF or by the legacy
// webhook-bridge marker.
func (h HostInfo) IsManagedByUs() bool {
	return isManagedByUs(h.ManagedBy)
}

// IsLegacyManagedByUs returns true if the host still uses the old marker.
func (h HostInfo) IsLegacyManagedByUs() bool {
	return isManagedByLegacy(h.ManagedBy)
}

// IsDummy returns true if the host uses the "dummy" check command.
func (h HostInfo) IsDummy() bool {
	return h.CheckCommand == "dummy"
}

// GetServiceInfo retrieves detailed service information from Icinga2.
func (c *APIClient) GetServiceInfo(host, service string) (ServiceInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getServiceInfo(host, service)
}

// GetHostInfo retrieves detailed host information from Icinga2.
func (c *APIClient) GetHostInfo(host string) (HostInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getHostInfo(host)
}

func (c *APIClient) getServiceInfo(host, service string) (ServiceInfo, error) {
	reqURL := fmt.Sprintf("%s/v1/objects/services/%s!%s", c.BaseURL, url.PathEscape(host), url.PathEscape(service))
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("icinga api: create request: %w", err)
	}
	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(http.MethodGet, reqURL, nil, 0, nil, time.Since(start), err)
		return ServiceInfo{}, fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(http.MethodGet, reqURL, nil, resp.StatusCode, respBody, time.Since(start), nil)

	if resp.StatusCode == http.StatusNotFound {
		return ServiceInfo{Exists: false, HostName: host, Name: service}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return ServiceInfo{}, fmt.Errorf("icinga api: check service %q on host %q: status %d: %s", service, host, resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []struct {
			Attrs struct {
				Name            string         `json:"name"`
				DisplayName     string         `json:"display_name"`
				CheckCommand    string         `json:"check_command"`
				State           float64        `json:"state"`
				Notes           string         `json:"notes"`
				Vars            map[string]any `json:"vars"`
				LastCheckResult *struct {
					State        float64 `json:"state"`
					Output       string  `json:"output"`
					ExecutionEnd float64 `json:"execution_end"`
				} `json:"last_check_result"`
			} `json:"attrs"`
		} `json:"results"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return ServiceInfo{}, fmt.Errorf("icinga api: decode service response: %w", err)
	}

	if len(result.Results) == 0 {
		return ServiceInfo{Exists: false, HostName: host, Name: service}, nil
	}

	r := result.Results[0].Attrs
	info := ServiceInfo{
		Exists:       true,
		HostName:     host,
		Name:         r.Name,
		DisplayName:  r.DisplayName,
		Notes:        r.Notes,
		CheckCommand: r.CheckCommand,
	}

	if r.Vars != nil {
		if mb, ok := r.Vars["managed_by"].(string); ok {
			info.ManagedBy = mb
		}
		if createdAt, ok := r.Vars["iaf_created_at"].(string); ok {
			info.BridgeCreatedAt = createdAt
		} else if createdAt, ok := r.Vars["bridge_created_at"].(string); ok {
			info.BridgeCreatedAt = createdAt
		}
	}

	info.ExitStatus = int(r.State)
	if r.LastCheckResult != nil {
		info.ExitStatus = int(r.LastCheckResult.State)
		info.Output = r.LastCheckResult.Output
		info.LastCheck = time.Unix(int64(r.LastCheckResult.ExecutionEnd), 0)
		info.HasCheckResult = true
	}

	return info, nil
}

func (c *APIClient) checkHostConflict(host string) (bool, error) {
	info, err := c.getHostInfo(host)
	if err != nil {
		return false, err
	}
	if !info.Exists {
		return false, nil
	}
	if info.IsManagedByUs() || info.IsDummy() || c.Force {
		return false, nil
	}
	return true, nil
}

func (c *APIClient) checkServiceConflict(host, service string) (bool, error) {
	info, err := c.getServiceInfo(host, service)
	if err != nil {
		return false, err
	}
	if !info.Exists {
		return false, nil
	}
	if info.IsManagedByUs() || info.CheckCommand == "dummy" || c.Force {
		return false, nil
	}
	return true, nil
}

func (c *APIClient) getHostInfo(host string) (HostInfo, error) {
	reqURL := fmt.Sprintf("%s/v1/objects/hosts/%s", c.BaseURL, url.PathEscape(host))
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return HostInfo{}, fmt.Errorf("icinga api: create request: %w", err)
	}
	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(http.MethodGet, reqURL, nil, 0, nil, time.Since(start), err)
		return HostInfo{}, fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(http.MethodGet, reqURL, nil, resp.StatusCode, respBody, time.Since(start), nil)

	if resp.StatusCode == http.StatusNotFound {
		return HostInfo{Exists: false}, nil
	}
	if resp.StatusCode != http.StatusOK {
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

	if err := json.Unmarshal(respBody, &result); err != nil {
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, err := c.getHostInfo(host)
	if err != nil {
		return false, err
	}
	return info.Exists, nil
}

// CreateHost creates a dummy host in Icinga2 via the REST API.
// The host is marked with IAF-specific vars so we can detect it on
// subsequent startups and avoid conflicts with Director.
func (c *APIClient) CreateHost(spec HostSpec) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	conflict, err := c.checkHostConflict(spec.Name)
	if err != nil {
		return err
	}
	if conflict {
		msg := fmt.Sprintf("host %q already exists and is not managed by the bridge (policy: %s)", spec.Name, c.ConflictPolicy)
		if c.ConflictPolicy == ConflictPolicyFail || c.ConflictPolicy == ConflictPolicySkip {
			return &ErrConflict{Message: msg}
		}
		// ConflictPolicyWarn falls through to proceed with creation
	}

	if spec.DisplayName == "" {
		spec.DisplayName = spec.Name
	}

	createdAt := time.Now().UTC().Format(time.RFC3339)

	vars := map[string]any{
		"managed_by":     ManagedByIAF,
		"iaf_managed":    true,
		"iaf_component":  ManagedByIAF,
		"iaf_created_at": createdAt,
	}
	if spec.Address != "" {
		// Keep the requested address only as metadata so generic apply rules
		// do not create ping/ssh services on this passive-only host.
		vars["iaf_host_address"] = spec.Address
	}

	mailVars := map[string]any{}
	if len(spec.Notification.Users) > 0 {
		mailVars["users"] = spec.Notification.Users
	}
	if len(spec.Notification.Groups) > 0 {
		mailVars["groups"] = spec.Notification.Groups
		mailVars["user_groups"] = spec.Notification.Groups
	}
	if len(spec.Notification.ServiceStates) > 0 {
		mailVars["service_states"] = spec.Notification.ServiceStates
	}
	if len(spec.Notification.HostStates) > 0 {
		mailVars["host_states"] = spec.Notification.HostStates
	}
	if len(mailVars) > 0 {
		notificationVars := cloneNotificationVars(mailVars)
		notificationVars["mail"] = cloneNotificationVars(mailVars)
		notificationVars["sms"] = cloneNotificationVars(mailVars)
		vars["notification"] = notificationVars
	}

	attrs := map[string]any{
		"attrs": map[string]any{
			"check_command":        "dummy",
			"enable_active_checks": false,
			"max_check_attempts":   1,
			"display_name":         spec.DisplayName,
			"vars":                 vars,
		},
		"templates": []string{"generic-host"},
	}

	body, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("icinga api: marshal host payload: %w", err)
	}

	reqURL := fmt.Sprintf("%s/v1/objects/hosts/%s", c.BaseURL, url.PathEscape(spec.Name))
	req, err := http.NewRequest(http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("icinga api: create request: %w", err)
	}

	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(http.MethodPut, reqURL, body, 0, nil, time.Since(start), err)
		return fmt.Errorf("icinga api: send create host request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(http.MethodPut, reqURL, body, resp.StatusCode, respBody, time.Since(start), nil)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("icinga api: create host %q: status %d: %s", spec.Name, resp.StatusCode, string(respBody))
	}

	return nil
}

func cloneNotificationVars(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// ListServices returns all services for the given host from Icinga2.
func (c *APIClient) ListServices(host string) ([]ServiceInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	filter := map[string]any{
		"filter": fmt.Sprintf(`host.name==%q`, host),
		"attrs":  []string{"name", "display_name", "state", "last_check_result", "notes", "vars"},
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

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(http.MethodPost, reqURL, body, 0, nil, time.Since(start), err)
		return nil, fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(http.MethodPost, reqURL, body, resp.StatusCode, respBody, time.Since(start), nil)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("icinga api: list services: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []struct {
			Name  string `json:"name"`
			Attrs struct {
				Name            string         `json:"name"`
				DisplayName     string         `json:"display_name"`
				State           float64        `json:"state"`
				Notes           string         `json:"notes"`
				Vars            map[string]any `json:"vars"`
				LastCheckResult *struct {
					State        float64 `json:"state"`
					Output       string  `json:"output"`
					ExecutionEnd float64 `json:"execution_end"`
				} `json:"last_check_result"`
			} `json:"attrs"`
		} `json:"results"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("icinga api: decode response: %w", err)
	}

	services := make([]ServiceInfo, 0, len(result.Results))
	for _, r := range result.Results {
		svc := ServiceInfo{
			HostName:    host,
			Name:        r.Attrs.Name,
			DisplayName: r.Attrs.DisplayName,
			Notes:       r.Attrs.Notes,
		}
		if mb, ok := r.Attrs.Vars["managed_by"].(string); ok {
			svc.ManagedBy = mb
		}
		if createdAt, ok := r.Attrs.Vars["iaf_created_at"].(string); ok {
			svc.BridgeCreatedAt = createdAt
		} else if createdAt, ok := r.Attrs.Vars["bridge_created_at"].(string); ok {
			svc.BridgeCreatedAt = createdAt
		}
		svc.ExitStatus = int(r.Attrs.State)
		if r.Attrs.LastCheckResult != nil {
			svc.ExitStatus = int(r.Attrs.LastCheckResult.State)
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
	Exists          bool      `json:"-"`
	HostName        string    `json:"host"`
	Name            string    `json:"name"`
	DisplayName     string    `json:"display_name"`
	Notes           string    `json:"notes"`
	ManagedBy       string    `json:"managed_by,omitempty"`
	BridgeCreatedAt string    `json:"bridge_created_at,omitempty"`
	ExitStatus      int       `json:"exit_status"`
	Output          string    `json:"output"`
	LastCheck       time.Time `json:"last_check"`
	HasCheckResult  bool      `json:"has_check_result"`
	CheckCommand    string    `json:"-"`
}

// IsManagedByUs returns true if the service is managed by IAF or by the legacy
// webhook-bridge marker.
func (s ServiceInfo) IsManagedByUs() bool {
	return isManagedByUs(s.ManagedBy)
}

// IsLegacyManagedByUs returns true if the service still uses the old marker.
func (s ServiceInfo) IsLegacyManagedByUs() bool {
	return isManagedByLegacy(s.ManagedBy)
}

// CreateService creates a dummy passive service in Icinga2 via the REST API.
// Uses PUT /v1/objects/services/<host>!<service> to create the object directly.
// Labels and annotations from the Grafana webhook are stored as Icinga2 attributes
// so operators can see the full alert context in the Icinga2 UI.
func (c *APIClient) CreateService(host, name string, labels, annotations map[string]string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	conflict, err := c.checkServiceConflict(host, name)
	if err != nil {
		return err
	}
	if conflict {
		msg := fmt.Sprintf("service %q on host %q already exists and is not managed by the bridge (policy: %s)", name, host, c.ConflictPolicy)
		if c.ConflictPolicy == ConflictPolicyFail || c.ConflictPolicy == ConflictPolicySkip {
			return &ErrConflict{Message: msg}
		}
		// ConflictPolicyWarn falls through to proceed with creation
	}

	// Build notes from annotations (summary + description)
	notes := "Managed by IcingaAlertingForge | auto-created"
	if s := annotations["summary"]; s != "" {
		notes = s
	}
	if d := annotations["description"]; d != "" {
		notes += "\n" + d
	}

	createdAt := time.Now().UTC().Format(time.RFC3339)

	// Store all labels and annotations as custom vars for full context and mark
	// the service as managed by IAF so we can safely identify it later.
	vars := map[string]any{
		"managed_by":        ManagedByIAF,
		"iaf_managed":       true,
		"iaf_component":     ManagedByIAF,
		"iaf_host":          host,
		"iaf_created_at":    createdAt,
		"bridge_host":       host,
		"bridge_created_at": createdAt,
	}
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

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(http.MethodPut, reqURL, body, 0, nil, time.Since(start), err)
		return fmt.Errorf("icinga api: send create request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(http.MethodPut, reqURL, body, resp.StatusCode, respBody, time.Since(start), nil)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("icinga api: create service %q: status %d: %s", name, resp.StatusCode, string(respBody))
	}

	return nil
}

// DeleteService removes a service from Icinga2 via the REST API.
// Uses DELETE /v1/objects/services/<host>!<service> with cascade=true.
func (c *APIClient) DeleteService(host, name string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	conflict, err := c.checkServiceConflict(host, name)
	if err != nil {
		return err
	}
	if conflict {
		// For deletion, if it's a conflict (not managed by us), we only proceed if Force is true.
		// Since checkServiceConflict already accounts for Force, if it returns true,
		// it means Force is false AND it's not managed by us.
		return &ErrConflict{Message: fmt.Sprintf("refusing to delete service %q on host %q: not managed by the bridge", name, host)}
	}

	reqURL := fmt.Sprintf("%s/v1/objects/services/%s!%s?cascade=1", c.BaseURL, url.PathEscape(host), url.PathEscape(name))
	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("icinga api: create delete request: %w", err)
	}

	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(http.MethodDelete, reqURL, nil, 0, nil, time.Since(start), err)
		return fmt.Errorf("icinga api: send delete request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(http.MethodDelete, reqURL, nil, resp.StatusCode, respBody, time.Since(start), nil)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("icinga api: delete service %q: status %d: %s", name, resp.StatusCode, string(respBody))
	}

	return nil
}

// GetServiceStatus queries Icinga2 for the current status of a service on a host.
func (c *APIClient) GetServiceStatus(host, service string) (exitStatus int, output string, checkTime time.Time, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	filter := map[string]any{
		"filter": fmt.Sprintf(`host.name==%q && service.name==%q`, host, service),
		"attrs":  []string{"state", "last_check_result"},
	}

	body, err := json.Marshal(filter)
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: marshal status filter: %w", err)
	}

	reqURL := fmt.Sprintf("%s/v1/objects/services", c.BaseURL)
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: create request: %w", err)
	}
	req.SetBasicAuth(c.User, c.Pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-HTTP-Method-Override", "GET")

	startT := time.Now()
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		c.recordDebug(http.MethodPost, reqURL, body, 0, nil, time.Since(startT), err)
		return 0, "", time.Time{}, fmt.Errorf("icinga api: send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	c.recordDebug(http.MethodPost, reqURL, body, resp.StatusCode, respBody, time.Since(startT), nil)

	if resp.StatusCode != http.StatusOK {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Results []struct {
			Attrs struct {
				State           float64 `json:"state"`
				LastCheckResult struct {
					State        float64 `json:"state"`
					Output       string  `json:"output"`
					ExecutionEnd float64 `json:"execution_end"`
				} `json:"last_check_result"`
			} `json:"attrs"`
		} `json:"results"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: decode response: %w", err)
	}

	if len(result.Results) == 0 {
		return 0, "", time.Time{}, fmt.Errorf("icinga api: service %q not found on host %q", service, host)
	}

	attrs := result.Results[0].Attrs
	r := attrs.LastCheckResult
	ts := time.Unix(int64(r.ExecutionEnd), 0)
	return int(attrs.State), r.Output, ts, nil
}
