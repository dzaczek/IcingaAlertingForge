# Branch Protection

Recommended required checks for the `main` branch. Configure via GitHub repository Settings → Branches → Branch protection rules.

## Required checks

- [ ] **lint** — golangci-lint (must pass)
- [ ] **test** — unit tests with race detector (must pass)
- [ ] **build** — multi-platform build verification (must pass)
- [ ] **Gitleaks** — secret scanning (must pass)
- [ ] **Go Vulncheck** — vulnerability scanning (must pass)
- [ ] **CodeQL** — SAST analysis (must pass)

## Recommended settings

| Setting | Value |
|---------|-------|
| Require pull request before merging | ✅ |
| Required approvals | 1 |
| Dismiss stale reviews | ✅ |
| Require status checks to pass | ✅ |
| Require conversation resolution | ✅ |
| Require signed commits | ❌ (optional) |
| Require linear history | ❌ (optional) |
| Do not allow bypassing | ✅ (admins included) |

## Environment secrets

| Secret | Purpose |
|--------|---------|
| `CODECOV_TOKEN` | Upload coverage reports to Codecov |
| `GITHUB_TOKEN` | Auto-provided by GitHub Actions |
