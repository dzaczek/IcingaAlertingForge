// Package rbac provides role-based access control for the admin dashboard.
// Three roles are supported:
//   - viewer: read-only access to dashboard, history, and status
//   - operator: viewer + ability to change service status, flush queues
//   - admin: full access including settings, user management, service deletion
package rbac

import (
	"crypto/subtle"
	"log/slog"
	"sync"
)

// Role defines the access level for a user.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

// Permission defines an action that can be authorized.
type Permission string

const (
	PermViewDashboard Permission = "view.dashboard"
	PermViewHistory   Permission = "view.history"
	PermViewStatus    Permission = "view.status"
	PermViewQueue     Permission = "view.queue"
	PermChangeStatus  Permission = "change.status"
	PermFlushQueue    Permission = "flush.queue"
	PermClearHistory  Permission = "clear.history"
	PermDeleteService Permission = "delete.service"
	PermManageConfig  Permission = "manage.config"
	PermManageUsers   Permission = "manage.users"
	PermDebugToggle   Permission = "debug.toggle"
)

// rolePermissions maps each role to its allowed permissions.
var rolePermissions = map[Role][]Permission{
	RoleViewer: {
		PermViewDashboard,
		PermViewHistory,
		PermViewStatus,
		PermViewQueue,
	},
	RoleOperator: {
		PermViewDashboard,
		PermViewHistory,
		PermViewStatus,
		PermViewQueue,
		PermChangeStatus,
		PermFlushQueue,
		PermClearHistory,
		PermDebugToggle,
	},
	RoleAdmin: {
		PermViewDashboard,
		PermViewHistory,
		PermViewStatus,
		PermViewQueue,
		PermChangeStatus,
		PermFlushQueue,
		PermClearHistory,
		PermDeleteService,
		PermManageConfig,
		PermManageUsers,
		PermDebugToggle,
	},
}

// User represents an authenticated user with a role.
type User struct {
	Username string `json:"username"`
	Password string `json:"-"` // hashed or plain (for env-based config)
	Role     Role   `json:"role"`
}

// Manager handles user authentication and authorization.
type Manager struct {
	mu    sync.RWMutex
	users map[string]User
}

// New creates a new RBAC manager with the given users.
func New(users []User) *Manager {
	m := &Manager{
		users: make(map[string]User, len(users)),
	}
	for _, u := range users {
		m.users[u.Username] = u
	}
	return m
}

// Authenticate validates username/password and returns the user if valid.
func (m *Manager) Authenticate(username, password string) (User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[username]
	if !ok {
		return User{}, false
	}

	if subtle.ConstantTimeCompare([]byte(password), []byte(user.Password)) != 1 {
		return User{}, false
	}

	return user, true
}

// Authorize checks if a user with the given role has the specified permission.
func (m *Manager) Authorize(role Role, perm Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}

// HasPermission checks if a user has a specific permission.
func (m *Manager) HasPermission(username string, perm Permission) bool {
	m.mu.RLock()
	user, ok := m.users[username]
	m.mu.RUnlock()

	if !ok {
		return false
	}
	return m.Authorize(user.Role, perm)
}

// GetUser returns user info (without password).
func (m *Manager) GetUser(username string) (User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, ok := m.users[username]
	if ok {
		user.Password = ""
	}
	return user, ok
}

// ListUsers returns all users (without passwords).
func (m *Manager) ListUsers() []User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	users := make([]User, 0, len(m.users))
	for _, u := range m.users {
		u.Password = ""
		users = append(users, u)
	}
	return users
}

// AddUser adds or updates a user.
func (m *Manager) AddUser(user User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[user.Username] = user
	slog.Info("RBAC: user added/updated", "username", user.Username, "role", user.Role)
}

// RemoveUser removes a user by username.
func (m *Manager) RemoveUser(username string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[username]; !ok {
		return false
	}
	delete(m.users, username)
	slog.Info("RBAC: user removed", "username", username)
	return true
}

// ParseRole converts a string to a Role, defaulting to viewer.
func ParseRole(s string) Role {
	switch Role(s) {
	case RoleAdmin:
		return RoleAdmin
	case RoleOperator:
		return RoleOperator
	default:
		return RoleViewer
	}
}
