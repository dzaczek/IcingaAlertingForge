# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| latest  | :white_check_mark: |
| < latest| :x:                |

Only the latest release receives security patches.

## Reporting a Vulnerability

**Do not open a public issue for security vulnerabilities.**

Report vulnerabilities via:

- **Email:** security@dzaczek.dev (preferred)
- **GitHub Security Advisories:** https://github.com/dzaczek/IcingaAlertingForge/security/advisories/new

You should receive a response within 72 hours. After the vulnerability is confirmed:

1. A fix will be developed and tested in a private fork
2. A GitHub Security Advisory will be published
3. A patch release will be issued
4. Credit will be given to the reporter (unless you prefer to remain anonymous)

## Scope

Security-sensitive areas of this project include:

- Webhook authentication and API key management (auth/, rbac/)
- Encrypted configuration storage (configstore/)
- Webhook payload parsing and validation (models/, handler/webhook.go)
- Icinga2 API authentication (icinga/)
- Dashboard authentication and session handling (handler/dashboard.go)

## Disclosure Policy

We follow a coordinated disclosure process. Please do not publicly disclose details before a fix is released.

We will acknowledge your report within 72 hours and provide an estimated timeline for a fix.
