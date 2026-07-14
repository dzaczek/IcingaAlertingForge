## 2024-05-24 - Host Parameter Gotcha
**Gotcha:** The `host` query parameter is optional in single-target deployments, but mandatory in multi-target setups for endpoints like `GET /status/{service_name}` and `DELETE /admin/services/{name}`.
**Resolution:** Always document that the `host` parameter must be provided if multiple hosts are configured, otherwise it will return a 400 Bad Request to prevent ambiguous operations.
## 2024-05-24 - Bulk Delete Legacy Support
**Gotcha:** The `POST /admin/services/bulk-delete` payload accepts a mixed JSON array (`[]string` vs `[]object`).
**Resolution:** Always document that while string arrays work for single-host setups, object arrays specifying both `host` and `service` are required for multi-host deployments to avoid ambiguity.
