## 2024-04-01 - [Missing Content-Security-Policy Header]
**Vulnerability:** The dashboard and web endpoints were lacking a Content-Security-Policy (CSP) header, relying only on X-XSS-Protection.
**Learning:** The application extensively uses inline scripts and styles within its HTML templates (e.g., `handler/dashboard.go`), meaning a strict CSP without `unsafe-inline` would break the UI.
**Prevention:** Added a baseline CSP (`default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;`) to `secureHandler` in `main.go`. In the future, refactoring inline scripts/styles into external files would allow for a stricter, more secure CSP.
## 2023-10-24 - [Plaintext Password Storage in RBAC]
**Vulnerability:** RBAC users' passwords were created and validated in plaintext using `subtle.ConstantTimeCompare`, exposing credentials if the dashboard config file is read.
**Learning:** External dependencies (like `golang.org/x/crypto/bcrypt`) cannot be downloaded due to strict network restrictions during builds. To securely store passwords without new dependencies, standard library functions (`crypto/sha256` + `crypto/rand`) must be used to create salted hashes, combined with a fallback mechanism to support pre-existing plaintext credentials from the environment.
**Prevention:** All new components that deal with authentication should use a `salt:hash` strategy utilizing standard library cryptography. Avoid using plain strings for passwords and handle backward compatibility safely to avoid breaking administrative lockouts.
