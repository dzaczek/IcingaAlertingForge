// Package rbac provides role-based access control for the admin dashboard.
// Three roles are supported:
//   - viewer: read-only access to dashboard, history, and status
//   - operator: viewer + ability to change service status, flush queues
//   - admin: full access including settings, user management, service deletion
package rbac

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"icinga-webhook-bridge/auth"
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
	mu      sync.RWMutex
	users   map[string]User
	primary string       // env-based primary admin username (cannot be deleted)
	onSave  func() error // optional persistence callback
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

// SetPrimary marks a username as the env-based primary admin (cannot be deleted).
func (m *Manager) SetPrimary(username string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.primary = username
}

// SetOnSave sets the persistence callback called after user mutations.
func (m *Manager) SetOnSave(fn func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSave = fn
}

// PersistableUsers returns all non-primary users for storage.
func (m *Manager) PersistableUsers() []User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// ⚡ Bolt: Pre-calculate total capacity to avoid repeated allocations during slice append
	out := make([]User, 0, len(m.users))
	for _, u := range m.users {
		if u.Username != m.primary {
			out = append(out, u)
		}
	}
	return out
}

// Authenticate validates username/password and returns the user if valid.
func (m *Manager) Authenticate(username, password string) (User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[username]
	if !ok {
		return User{}, false
	}

	// Bcrypt hash (starts with $2a$)
	if len(user.Password) >= 4 && user.Password[:4] == "$2a$" {
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
			return User{}, false
		}
		return user, true
	}

	// Legacy: SHA-256 "salt:hash" format (salt is 16 bytes = 32 hex chars, hash = 64 hex chars, plus ':' = 97 chars)
	if len(user.Password) == 97 && user.Password[32] == ':' {
		saltHex := user.Password[:32]
		hashHex := user.Password[33:]

		expectedHash := hashPasswordWithSalt(password, saltHex)
		if !auth.SecureCompare(expectedHash, hashHex) {
			return User{}, false
		}
		return user, true
	}

	// Fallback: plaintext comparison (e.g. from env) — constant-time to prevent timing leaks
	if !auth.SecureCompare(password, user.Password) {
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

// AddUser adds or updates a user and persists the change.
func (m *Manager) AddUser(user User) error {
	m.mu.Lock()
	// Hash password if not already hashed (bcrypt or legacy format)
	if len(user.Password) >= 4 && user.Password[:4] == "$2a$" {
		// already bcrypt-hashed
	} else if len(user.Password) == 97 && user.Password[32] == ':' {
		// already legacy SHA-256 hashed
	} else {
		hashed, err := hashPassword(user.Password)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("rbac: failed to hash password: %w", err)
		}
		user.Password = hashed
	}

	m.users[user.Username] = user
	onSave := m.onSave
	m.mu.Unlock()
	slog.Info("RBAC: user added/updated", "username", user.Username, "role", user.Role)
	if onSave != nil {
		return onSave()
	}
	return nil
}

// RemoveUser removes a user by username. Returns false if user not found.
// The primary admin (from env) cannot be deleted.
func (m *Manager) RemoveUser(username string) (bool, error) {
	m.mu.Lock()
	if username == m.primary {
		m.mu.Unlock()
		return false, fmt.Errorf("cannot delete primary admin user")
	}
	if _, ok := m.users[username]; !ok {
		m.mu.Unlock()
		return false, nil
	}
	delete(m.users, username)
	onSave := m.onSave
	m.mu.Unlock()
	slog.Info("RBAC: user removed", "username", username)
	if onSave != nil {
		if err := onSave(); err != nil {
			return true, err
		}
	}
	return true, nil
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

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func hashPasswordWithSalt(password, saltHex string) string {
	h := sha256.New()
	h.Write([]byte(saltHex + password))
	return hex.EncodeToString(h.Sum(nil))
}
