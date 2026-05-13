package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"icinga-webhook-bridge/cache"
	"icinga-webhook-bridge/icinga"
	"icinga-webhook-bridge/queue"
	"icinga-webhook-bridge/rbac"
)

// ── HandleRateLimitStats ────────────────────────────────────────────────────

func TestAdmin_HandleRateLimitStats(t *testing.T) {
	h, _ := testAdminHandler(t, nil)

	t.Run("limiter nil returns status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/ratelimit", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleRateLimitStats(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("with limiter", func(t *testing.T) {
		h.Limiter = icinga.NewRateLimiter(3, 20, 100)
		req := httptest.NewRequest(http.MethodGet, "/admin/ratelimit", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleRateLimitStats(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		var resp map[string]any
		json.NewDecoder(rr.Body).Decode(&resp)
		if _, ok := resp["mutate"]; !ok {
			t.Error("expected mutate key in response")
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/ratelimit", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleRateLimitStats(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

// ── HandleSetServiceStatus ──────────────────────────────────────────────────

func TestAdmin_HandleSetServiceStatus(t *testing.T) {
	checkResultCalled := false
	h, _ := testAdminHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			checkResultCalled = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"results":[{"code":200}]}`))
		}
	})

	t.Run("valid status change", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"host":        "host-a",
			"exit_status": 2,
			"output":      "CRITICAL: manual test",
		})
		req := httptest.NewRequest(http.MethodPost, "/admin/services/svc-test/status", bytes.NewBuffer(body))
		req.URL.Path = "/admin/services/svc-test/status"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleSetServiceStatus(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		if !checkResultCalled {
			t.Error("check result API was not called")
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/services/svc/status", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleSetServiceStatus(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})

	t.Run("invalid exit_status", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"host": "host-a", "exit_status": 99})
		req := httptest.NewRequest(http.MethodPost, "/admin/services/svc-test/status", bytes.NewBuffer(body))
		req.URL.Path = "/admin/services/svc-test/status"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleSetServiceStatus(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})
}

// ── HandleQueueStats / HandleQueueFlush ─────────────────────────────────────

func newTestQueue(t *testing.T) *queue.Queue {
	t.Helper()
	return queue.New(queue.Config{}, nil)
}

func TestAdmin_HandleQueueStats(t *testing.T) {
	h, _ := testAdminHandler(t, nil)

	t.Run("queue nil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/queue", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleQueueStats(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("queue present", func(t *testing.T) {
		h.RetryQueue = newTestQueue(t)
		req := httptest.NewRequest(http.MethodGet, "/admin/queue", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleQueueStats(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/queue", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleQueueStats(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestAdmin_HandleQueueFlush(t *testing.T) {
	h, _ := testAdminHandler(t, nil)

	t.Run("queue nil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/queue/flush", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleQueueFlush(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("queue present", func(t *testing.T) {
		h.RetryQueue = newTestQueue(t)
		req := httptest.NewRequest(http.MethodPost, "/admin/queue/flush", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleQueueFlush(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/queue/flush", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleQueueFlush(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

// ── HandleListUsers / HandleCreateUser / HandleDeleteUser ───────────────────

func TestAdmin_HandleListUsers(t *testing.T) {
	h, _ := testAdminHandler(t, nil)

	t.Run("empty list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleListUsers(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("RBAC nil", func(t *testing.T) {
		h2, _ := testAdminHandler(t, nil)
		h2.RBAC = nil
		req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h2.HandleListUsers(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/users", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleListUsers(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestAdmin_HandleCreateUser(t *testing.T) {
	h, _ := testAdminHandler(t, nil)

	t.Run("creates user successfully", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"username": "newuser",
			"password": "pass123",
			"role":     "viewer",
		})
		req := httptest.NewRequest(http.MethodPost, "/admin/users", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleCreateUser(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("missing username", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"password": "pass"})
		req := httptest.NewRequest(http.MethodPost, "/admin/users", bytes.NewBuffer(body))
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleCreateUser(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleCreateUser(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestAdmin_HandleDeleteUser(t *testing.T) {
	h, rbacMgr := testAdminHandler(t, nil)
	rbacMgr.AddUser(rbac.User{Username: "todelete", Password: "pass", Role: rbac.RoleViewer})

	t.Run("deletes existing user", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/users/todelete", nil)
		req.URL.Path = "/admin/users/todelete"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteUser(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("user not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/users/nonexistent", nil)
		req.URL.Path = "/admin/users/nonexistent"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteUser(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rr.Code)
		}
	})

	t.Run("self-deletion rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/admin/users/admin", nil)
		req.URL.Path = "/admin/users/admin"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteUser(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rr.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/users/u", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleDeleteUser(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

// ── HandleFreezeService / HandleListFrozen ──────────────────────────────────

func TestAdmin_HandleFreezeService(t *testing.T) {
	h, _ := testAdminHandler(t, nil)

	t.Run("freeze permanently", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"host": "host-a", "duration_seconds": 0})
		req := httptest.NewRequest(http.MethodPost, "/admin/services/svc-freeze/freeze", bytes.NewBuffer(body))
		req.URL.Path = "/admin/services/svc-freeze/freeze"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleFreezeService(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		if !h.Cache.IsFrozen("host-a", "svc-freeze") {
			t.Error("service should be frozen")
		}
	})

	t.Run("freeze with duration", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"host": "host-a", "duration_seconds": 3600})
		req := httptest.NewRequest(http.MethodPost, "/admin/services/svc-dur/freeze", bytes.NewBuffer(body))
		req.URL.Path = "/admin/services/svc-dur/freeze"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleFreezeService(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var resp map[string]any
		json.NewDecoder(rr.Body).Decode(&resp)
		if _, ok := resp["frozen_until"]; !ok {
			t.Error("expected frozen_until in response")
		}
	})

	t.Run("unfreeze", func(t *testing.T) {
		// first freeze
		h.Cache.Freeze("host-a", "svc-unfreeze", nil)

		body, _ := json.Marshal(map[string]any{"host": "host-a"})
		req := httptest.NewRequest(http.MethodDelete, "/admin/services/svc-unfreeze/freeze", bytes.NewBuffer(body))
		req.URL.Path = "/admin/services/svc-unfreeze/freeze"
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleFreezeService(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
		if h.Cache.IsFrozen("host-a", "svc-unfreeze") {
			t.Error("service should be unfrozen after DELETE")
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/services/svc/freeze", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleFreezeService(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

func TestAdmin_HandleListFrozen(t *testing.T) {
	h, _ := testAdminHandler(t, nil)
	h.Cache = cache.NewServiceCache(60)

	expiry := time.Now().Add(time.Hour)
	h.Cache.Freeze("host-a", "svc-a", nil)
	h.Cache.Freeze("host-a", "svc-b", &expiry)

	req := httptest.NewRequest(http.MethodGet, "/admin/services/frozen", nil)
	req.SetBasicAuth("admin", "secret")
	rr := httptest.NewRecorder()
	h.HandleListFrozen(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	frozen, ok := resp["frozen"]
	if !ok {
		t.Fatal("expected frozen key in response")
	}
	items := frozen.([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 frozen items, got %d", len(items))
	}

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/services/frozen", nil)
		req.SetBasicAuth("admin", "secret")
		rr := httptest.NewRecorder()
		h.HandleListFrozen(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})
}

// ── checkAuth edge cases ────────────────────────────────────────────────────

func TestAdmin_checkAuth_NoPass(t *testing.T) {
	h := &AdminHandler{Pass: ""}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	if h.checkAuth(rr, req) {
		t.Error("expected false when ADMIN_PASS not configured")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}
