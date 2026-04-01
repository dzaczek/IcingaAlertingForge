## 2024-04-01 - [Missing Content-Security-Policy Header]
**Vulnerability:** The dashboard and web endpoints were lacking a Content-Security-Policy (CSP) header, relying only on X-XSS-Protection.
**Learning:** The application extensively uses inline scripts and styles within its HTML templates (e.g., `handler/dashboard.go`), meaning a strict CSP without `unsafe-inline` would break the UI.
**Prevention:** Added a baseline CSP (`default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;`) to `secureHandler` in `main.go`. In the future, refactoring inline scripts/styles into external files would allow for a stricter, more secure CSP.
