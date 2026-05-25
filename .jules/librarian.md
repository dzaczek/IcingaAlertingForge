## 2024-05-24 - Host Parameter Gotcha
**Gotcha:** The `host` query parameter is optional in single-target deployments, but mandatory in multi-target setups for endpoints like `GET /status/{service_name}` and `DELETE /admin/services/{name}`.
**Resolution:** Always document that the `host` parameter must be provided if multiple hosts are configured, otherwise it will return a 400 Bad Request to prevent ambiguous operations.
