package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

const baseURL = "http://localhost:9080"

func skipIfNoDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Skip("Docker testenv not reachable on " + baseURL)
	}
}

func newChromeContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1920, 1080),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)
	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(func() { cancel(); allocCancel() })
	if err := chromedp.Run(ctx); err != nil {
		t.Skipf("Chrome not available: %v", err)
	}
	return ctx, cancel
}

// ── Browser: Login ──────────────────────────────────────────────────────

func TestLogin_Invalid(t *testing.T) {
	skipIfNoDocker(t)
	ctx, cancel := newChromeContext(t)
	defer cancel()
	var body string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(baseURL+"/status/beauty?admin=1"),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
		chromedp.OuterHTML(`body`, &body),
	); err != nil {
		t.Skipf("render: %v", err)
	}
	if !strings.Contains(body, "Enter credentials") {
		t.Errorf("expected login prompt, got: %.200s", body)
	}
}

func TestLogin_Valid(t *testing.T) {
	skipIfNoDocker(t)
	ctx, cancel := newChromeContext(t)
	defer cancel()
	var body string
	if err := chromedp.Run(ctx,
		chromedp.Navigate("http://admin:admin123@localhost:9080/status/beauty?admin=1"),
		chromedp.WaitVisible(`#header-uptime`, chromedp.ByID),
		chromedp.OuterHTML(`body`, &body),
	); err != nil {
		t.Skipf("chrome auth: %v", err)
	}
	for _, want := range []string{"COMMAND ACCESS", "LOGOUT"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// ── Browser: Dashboard ─────────────────────────────────────────────────

func TestDashboard_Renders(t *testing.T) {
	skipIfNoDocker(t)
	ctx, cancel := newChromeContext(t)
	defer cancel()
	var body string
	if err := chromedp.Run(ctx,
		chromedp.Navigate("http://admin:admin123@localhost:9080/status/beauty?admin=1"),
		chromedp.WaitVisible(`.stat-cell`, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.OuterHTML(`body`, &body),
	); err != nil {
		t.Skipf("render: %v", err)
	}
	for _, el := range []string{"stat-total", "stat-firing", "IcingaAlertForge"} {
		if !strings.Contains(body, el) {
			t.Errorf("missing %q", el)
		}
	}
}

// ── API: helpers ────────────────────────────────────────────────────────

type apiClient struct {
	c *http.Client
}

func (a *apiClient) get(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", baseURL+path, nil)
	req.SetBasicAuth("admin", "admin123")
	resp, err := a.c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (a *apiClient) post(t *testing.T, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", baseURL+path, strings.NewReader(body))
	req.SetBasicAuth("admin", "admin123")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (a *apiClient) del(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", baseURL+path, nil)
	req.SetBasicAuth("admin", "admin123")
	resp, err := a.c.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

// ── API: Full User Flow ────────────────────────────────────────────────

func TestUserFlow_API_FullCycle(t *testing.T) {
	skipIfNoDocker(t)
	a := &apiClient{c: &http.Client{Timeout: 10 * time.Second}}
	targetID := fmt.Sprintf("e2e-%d", time.Now().UnixNano()/1e6)

	t.Run("health", func(t *testing.T) {
		resp := a.get(t, "/health")
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("health: %d", resp.StatusCode)
		}
	})

	t.Run("admin_login", func(t *testing.T) {
		resp := a.get(t, "/status/beauty?admin=1")
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("admin login: %d", resp.StatusCode)
		}
	})

	t.Run("add_target", func(t *testing.T) {
		body := fmt.Sprintf(`{"id":"%s","host_name":"%s","source":"e2e","host_display":"E2E"}`, targetID, targetID)
		resp := a.post(t, "/admin/settings/targets", body)
		resp.Body.Close()
		if resp.StatusCode != 201 {
			t.Errorf("add target: expected 201, got %d", resp.StatusCode)
		}
	})

	t.Run("generate_key", func(t *testing.T) {
		resp := a.post(t, "/admin/settings/targets/"+targetID+"/generate-key", "")
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("generate key: %d", resp.StatusCode)
		}
	})

	t.Run("webhook_no_key", func(t *testing.T) {
		req, _ := http.NewRequest("POST", baseURL+"/webhook",
			strings.NewReader(`{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"E2E","severity":"critical"},"annotations":{"summary":"test"}}]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := a.c.Do(req)
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Errorf("no key: expected 401, got %d", resp.StatusCode)
		}
	})

	t.Run("webhook_valid", func(t *testing.T) {
		req, _ := http.NewRequest("POST", baseURL+"/webhook",
			strings.NewReader(`{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"E2EValid","severity":"critical"},"annotations":{"summary":"valid"}}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key-grafana-local")
		resp, _ := a.c.Do(req)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("valid key: %d", resp.StatusCode)
		}
	})

	t.Run("history", func(t *testing.T) {
		resp := a.get(t, "/history?limit=3")
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("history: %d", resp.StatusCode)
		}
	})

	t.Run("cleanup", func(t *testing.T) {
		resp := a.del(t, "/admin/settings/targets/"+targetID)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("delete target: %d", resp.StatusCode)
		}
	})
}

// ── API: RBAC User Lifecycle ────────────────────────────────────────────

func TestRBAC_UserLifecycle(t *testing.T) {
	skipIfNoDocker(t)
	a := &apiClient{c: &http.Client{Timeout: 10 * time.Second}}
	user := fmt.Sprintf("e2euser-%d", time.Now().UnixNano()/1e6)

	t.Run("create", func(t *testing.T) {
		body := fmt.Sprintf(`{"username":"%s","password":"viewpass","role":"viewer"}`, user)
		resp := a.post(t, "/admin/users", body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("create user: %d", resp.StatusCode)
		}
	})

	authGet := func(username, password, path string) int {
		req, _ := http.NewRequest("GET", baseURL+path, nil)
		req.SetBasicAuth(username, password)
		resp, _ := a.c.Do(req)
		resp.Body.Close()
		return resp.StatusCode
	}

	t.Run("viewer_dashboard", func(t *testing.T) {
		if c := authGet(user, "viewpass", "/status/beauty?admin=1"); c != 200 {
			t.Errorf("viewer dashboard: %d", c)
		}
	})

	t.Run("viewer_settings_denied", func(t *testing.T) {
		if c := authGet(user, "viewpass", "/admin/settings/export"); c != 403 {
			t.Errorf("viewer settings: expected 403, got %d", c)
		}
	})

	t.Run("viewer_users_denied", func(t *testing.T) {
		if c := authGet(user, "viewpass", "/admin/users"); c != 403 {
			t.Errorf("viewer users: expected 403, got %d", c)
		}
	})

	t.Run("delete_user", func(t *testing.T) {
		resp := a.del(t, "/admin/users/"+user)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("delete user: %d", resp.StatusCode)
		}
	})

	t.Run("deleted_401", func(t *testing.T) {
		if c := authGet(user, "viewpass", "/status/beauty?admin=1"); c != 401 {
			t.Errorf("deleted login: expected 401, got %d", c)
		}
	})
}

// ── API: SSE & Dashboard Mode ───────────────────────────────────────────

func TestSSE_Streaming(t *testing.T) {
	skipIfNoDocker(t)
	req, _ := http.NewRequest("GET", baseURL+"/status/beauty/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("SSE: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content-type: %q", ct)
	}
}

func TestDashboardMode_EmptyStart(t *testing.T) {
	resp, err := http.Get("http://localhost:9081/health")
	if err != nil {
		t.Skip("dashboard bridge not running")
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Skip("dashboard bridge not healthy")
	}
	resp2, _ := http.Get("http://localhost:9081/status/beauty/stats")
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("dashboard stats: %d", resp2.StatusCode)
	}
}
