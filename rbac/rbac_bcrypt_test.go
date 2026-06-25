package rbac

import (
	"strings"
	"testing"
)

func TestAddUser_Exceeds72Bytes(t *testing.T) {
	m := New(nil)
	longPass := strings.Repeat("a", 73)

	err := m.AddUser(User{Username: "testuser", Password: longPass, Role: RoleViewer})
	if err == nil {
		t.Errorf("Expected error for long password, got nil")
	} else if !strings.Contains(err.Error(), "bcrypt: password length exceeds 72 bytes") {
		t.Errorf("Expected bcrypt length error, got: %v", err)
	}
}
