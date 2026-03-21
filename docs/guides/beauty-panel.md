# Beauty Panel

Back to the [Documentation Index](../README.md)

## Where It Lives

- public view: `/status/beauty`
- admin view: `/status/beauty?admin=1`

<!-- LANG: hyphenation -->
The panel is an LCARS-style HTML dashboard. It is not meant to look like a generic admin table.

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

Example public views:

Overview:

![Beauty panel overview](../img/beauty-panel-overview.png)

Recent alerts:

![Beauty panel alerts](../img/beauty-panel-alerts.png)

Errors:

![Beauty panel errors](../img/beauty-panel-errors.png)

## Admin View

Admin mode adds:

- runtime and process metrics
- request, auth, and security metrics
- the current Icinga service table across all configured hosts
- a host column in the table
<!-- CHANGED: added Dev Panel and service history to admin features -->
- single delete and bulk delete actions
- the Dev Panel for live API traffic inspection
- service history popup on service tags

Example admin views:

Service management:

![Beauty panel admin services](../img/beauty-panel-admin-services.png)

System metrics:

![Beauty panel admin system](../img/beauty-panel-admin-system.png)

Admin mode uses the same HTTP Basic Auth credentials as the admin API.

<!-- CHANGED: added Dev Panel, Service History Popup, SSE, and Info Popup documentation -->

### Dev Panel

The Dev Panel is an admin-only section for inspecting live API traffic between Grafana and Icinga2. Navigate to it using `#devpanel` or the sidebar button.

Controls:

- **Debug ON/OFF** — toggles debug capture on the backend via `POST /admin/debug/toggle`
- **Pause/Resume** — pauses or resumes the SSE stream display (client-side only, backend keeps capturing)
- **Clear** — clears displayed entries from the panel

Each entry shows:

- **Direction** — `IN` (Grafana to IcingaAlertForge) or `OUT` (IcingaAlertForge to Icinga2)
- **Method and URL**
- **Status code and duration**
- **Timestamps**
- **Request/response bodies** with JSON syntax highlighting and a copy button

### Service History Popup

Click any service tag in the Services section to see its last 5 status changes. The popup fetches data from `/history?service=NAME&limit=5` and shows:

- Timestamp
- Action (`firing`, `resolved`, `create`)
- Exit status code
- Message

### SSE Real-time Updates

The dashboard uses Server-Sent Events (`/status/beauty/events`) for live updates. Webhook flow animations and debug stream entries arrive in real-time without polling.

### Info Popups

The `?` icons on panel section headers display LCARS-themed information popups explaining each section.

## Multi Host Behaviour

<!-- LANG: hyphenation -->
The panel reflects the current multi-host setup:

- services are shown across all managed hosts
- the service table includes `host`
- cache chips show `host / service`
- history rows include the host name

## Navigation

The panel uses URL hashes such as:

<!-- CHANGED: added devpanel hash -->
- `#overview`
- `#alerts`
- `#errors`
- `#icinga`
- `#devpanel`

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

---
**Next step →** [Test Environment](test-environment.md)
