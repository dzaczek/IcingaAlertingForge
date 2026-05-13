package rbac

import (
	"errors"
	"testing"
)

func TestSetOnSave(t *testing.T) {
	m := New(nil)

	called := false
	m.SetOnSave(func() error {
		called = true
		return nil
	})

	// AddUser should trigger the onSave callback
	if err := m.AddUser(User{Username: "u1", Password: "pass", Role: RoleViewer}); err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}
	if !called {
		t.Error("onSave callback was not called by AddUser")
	}
}

func TestSetOnSave_Error(t *testing.T) {
	m := New(nil)
	m.SetOnSave(func() error {
		return errors.New("persistence failed")
	})

	err := m.AddUser(User{Username: "u2", Password: "pass", Role: RoleViewer})
	if err == nil {
		t.Error("expected error from onSave callback propagation")
	}
}
