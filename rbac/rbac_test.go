package rbac

import (
	"testing"
)

func TestAuthenticate(t *testing.T) {
	m := New([]User{
		{Username: "admin", Password: "secret123", Role: RoleAdmin},
		{Username: "operator", Password: "op-pass", Role: RoleOperator},
		{Username: "viewer", Password: "view-pass", Role: RoleViewer},
	})

	tests := []struct {
		name     string
		user     string
		pass     string
		wantOK   bool
		wantRole Role
	}{
		{"valid admin", "admin", "secret123", true, RoleAdmin},
		{"valid operator", "operator", "op-pass", true, RoleOperator},
		{"valid viewer", "viewer", "view-pass", true, RoleViewer},
		{"wrong password", "admin", "wrong", false, ""},
		{"unknown user", "nobody", "pass", false, ""},
		{"empty creds", "", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, ok := m.Authenticate(tt.user, tt.pass)
			if ok != tt.wantOK {
				t.Errorf("Authenticate(%s) ok=%v, want %v", tt.user, ok, tt.wantOK)
			}
			if ok && user.Role != tt.wantRole {
				t.Errorf("expected role %s, got %s", tt.wantRole, user.Role)
			}
		})
	}
}

func TestAuthorize(t *testing.T) {
	m := New(nil)

	tests := []struct {
		role Role
		perm Permission
		want bool
	}{
		// Viewer
		{RoleViewer, PermViewDashboard, true},
		{RoleViewer, PermViewHistory, true},
		{RoleViewer, PermChangeStatus, false},
		{RoleViewer, PermDeleteService, false},
		{RoleViewer, PermManageConfig, false},

		// Operator
		{RoleOperator, PermViewDashboard, true},
		{RoleOperator, PermChangeStatus, true},
		{RoleOperator, PermFlushQueue, true},
		{RoleOperator, PermDeleteService, false},
		{RoleOperator, PermManageConfig, false},

		// Admin
		{RoleAdmin, PermViewDashboard, true},
		{RoleAdmin, PermChangeStatus, true},
		{RoleAdmin, PermDeleteService, true},
		{RoleAdmin, PermManageConfig, true},
		{RoleAdmin, PermManageUsers, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.role)+"/"+string(tt.perm), func(t *testing.T) {
			got := m.Authorize(tt.role, tt.perm)
			if got != tt.want {
				t.Errorf("Authorize(%s, %s) = %v, want %v", tt.role, tt.perm, got, tt.want)
			}
		})
	}
}

func TestHasPermission(t *testing.T) {
	m := New([]User{
		{Username: "op1", Password: "pass", Role: RoleOperator},
	})

	if !m.HasPermission("op1", PermChangeStatus) {
		t.Error("operator should have change.status")
	}
	if m.HasPermission("op1", PermDeleteService) {
		t.Error("operator should NOT have delete.service")
	}
	if m.HasPermission("unknown", PermViewDashboard) {
		t.Error("unknown user should have no permissions")
	}
}

func TestAddRemoveUser(t *testing.T) {
	m := New(nil)

	if err := m.AddUser(User{Username: "new-user", Password: "pass", Role: RoleViewer}); err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}

	user, ok := m.GetUser("new-user")
	if !ok {
		t.Fatal("expected user to exist")
	}
	if user.Role != RoleViewer {
		t.Errorf("expected viewer role, got %s", user.Role)
	}
	if user.Password != "" {
		t.Error("GetUser should not return password")
	}

	// Update role
	if err := m.AddUser(User{Username: "new-user", Password: "pass2", Role: RoleAdmin}); err != nil {
		t.Fatalf("AddUser update failed: %v", err)
	}
	user, _ = m.GetUser("new-user")
	if user.Role != RoleAdmin {
		t.Errorf("expected admin role after update, got %s", user.Role)
	}

	// Remove
	removed, err := m.RemoveUser("new-user")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !removed {
		t.Error("expected successful removal")
	}
	removed, _ = m.RemoveUser("new-user")
	if removed {
		t.Error("expected false for non-existent user")
	}
}

func TestPrimaryCannotBeDeleted(t *testing.T) {
	m := New([]User{
		{Username: "admin", Password: "pass", Role: RoleAdmin},
		{Username: "op", Password: "pass", Role: RoleOperator},
	})
	m.SetPrimary("admin")

	_, err := m.RemoveUser("admin")
	if err == nil {
		t.Error("expected error when deleting primary admin")
	}

	removed, err := m.RemoveUser("op")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !removed {
		t.Error("expected successful removal of non-primary user")
	}
}

func TestPersistableUsers(t *testing.T) {
	m := New([]User{
		{Username: "admin", Password: "pass", Role: RoleAdmin},
		{Username: "viewer1", Password: "pass", Role: RoleViewer},
	})
	m.SetPrimary("admin")

	users := m.PersistableUsers()
	if len(users) != 1 {
		t.Fatalf("expected 1 persistable user, got %d", len(users))
	}
	if users[0].Username != "viewer1" {
		t.Errorf("expected viewer1, got %s", users[0].Username)
	}
}

func TestListUsers(t *testing.T) {
	m := New([]User{
		{Username: "a", Password: "x", Role: RoleAdmin},
		{Username: "b", Password: "y", Role: RoleViewer},
	})

	users := m.ListUsers()
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	for _, u := range users {
		if u.Password != "" {
			t.Error("ListUsers should strip passwords")
		}
	}
}

func TestParseRole(t *testing.T) {
	if ParseRole("admin") != RoleAdmin {
		t.Error("expected admin")
	}
	if ParseRole("operator") != RoleOperator {
		t.Error("expected operator")
	}
	if ParseRole("viewer") != RoleViewer {
		t.Error("expected viewer")
	}
	if ParseRole("unknown") != RoleViewer {
		t.Error("expected viewer as default")
	}
	if ParseRole("") != RoleViewer {
		t.Error("expected viewer for empty string")
	}
}
