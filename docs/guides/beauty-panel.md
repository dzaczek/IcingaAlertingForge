# Beauty Panel

Back to the [Documentation Index](../README.md)

## Where It Lives

- public view: `/status/beauty`
- admin view: `/status/beauty?admin=1`

The panel is an LCARS style HTML dashboard. It is not meant to look like a generic admin table.

## Public View

The public panel shows:

- total webhook count
- error count
- average duration
- cached service count
- mode and severity breakdowns
- source counters
- recent alerts
- recent errors
- cache entries across all managed hosts

## Admin View

Admin mode adds:

- runtime and process metrics
- request, auth, and security metrics
- the current Icinga service table across all configured hosts
- a host column in the table
- single delete and bulk delete actions

Admin mode uses the same HTTP Basic Auth credentials as the admin API.

## Multi Host Behaviour

The panel reflects the current multi host setup:

- services are shown across all managed hosts
- the service table includes `host`
- cache chips show `host / service`
- history rows include the host name

## Navigation

The panel uses URL hashes such as:

- `#overview`
- `#alerts`
- `#errors`
- `#icinga`

The navigation code was simplified to stop the old problem where the panel jumped between sections during refreshes.

## Screenshots

You do not need screenshots from a user machine to document the panel. The test environment already produces repeatable data, so screenshots can be taken directly from the local lab.

If you want screenshots that match the current UI:

1. start `testenv`
2. wait until Grafana and the bridge are healthy
3. open the public and admin panel views
4. wait for the synthetic alerts to populate recent activity and cache tables
5. capture the browser window

For the full workflow, see [Test Environment](test-environment.md).
