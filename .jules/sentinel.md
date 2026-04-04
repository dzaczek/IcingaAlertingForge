## 2024-04-01 - [Missing Content-Security-Policy Header]
**Vulnerability:** The dashboard and web endpoints were lacking a Content-Security-Policy (CSP) header, relying only on X-XSS-Protection.
**Learning:** The application extensively uses inline scripts and styles within its HTML templates (e.g., `handler/dashboard.go`), meaning a strict CSP without `unsafe-inline` would break the UI.
**Prevention:** Added a baseline CSP (`default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;`) to `secureHandler` in `main.go`. In the future, refactoring inline scripts/styles into external files would allow for a stricter, more secure CSP.

## 2024-05-01 - [XSS Vulnerability in UI Templates]
**Vulnerability:** The `escHtml` function inside `handler/dashboard.go` did not escape single quotes `'` and double quotes `"`, allowing potential Cross-Site Scripting (XSS) when untrusted input is interpolated into HTML attributes.
**Learning:** Even custom simple HTML escaper functions must be complete. It is very easy to forget escaping quotes, which are just as dangerous as `<` and `>` when injecting content into attributes like `title=""` or `onclick=""`.
**Prevention:** Ensured the `escHtml` utility handles double and single quotes (`&quot;` and `&#39;`). We should ideally use standard libraries like Go's `html/template` or the JS equivalent instead of custom string replacements, or strictly review regex replacements for completeness.
