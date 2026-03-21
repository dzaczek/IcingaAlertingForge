# Fast Track Deployment

Back to the [Documentation Index](../README.md)

## Goal

This guide is for the shortest path to a working deployment.

It assumes:

- you already have Icinga2 with the REST API enabled
- you already have Grafana Unified Alerting
- you want the bridge running quickly with one or two dummy hosts
- you can refine the setup later

If you want the full background first, read [Architecture and Setup](architecture-and-setup.md). If you just want something working today, stay here.

## Fastest Working Path

The quickest practical route is:

1. create a small `.env`
2. build or pull the container
3. run the bridge
4. create one Grafana contact point with an API key
5. send one test alert
6. confirm that the service appears in Icinga

## Minimal `.env`

Start with something close to this:

```env
SERVER_PORT=8080
SERVER_HOST=0.0.0.0

ICINGA2_HOST=https://icinga2.example.com:5665
ICINGA2_USER=apiuser
ICINGA2_PASS=change-me
ICINGA2_HOST_AUTO_CREATE=true
ICINGA2_TLS_SKIP_VERIFY=false

IAF_TARGET_HOME_CRITICAL_SOURCE=home-critical
IAF_TARGET_HOME_CRITICAL_HOST_NAME=home-critical
IAF_TARGET_HOME_CRITICAL_HOST_DISPLAY=Home Critical Alerts
IAF_TARGET_HOME_CRITICAL_API_KEYS=replace-with-long-random-key
IAF_TARGET_HOME_CRITICAL_NOTIFICATION_GROUPS=sms-admins
IAF_TARGET_HOME_CRITICAL_NOTIFICATION_SERVICE_STATES=critical
IAF_TARGET_HOME_CRITICAL_NOTIFICATION_HOST_STATES=down

ADMIN_USER=admin
ADMIN_PASS=change-me
```

What this gives you:

- one managed dummy host in Icinga named `home-critical`
- one API key for Grafana
- notifications routed through one Icinga group
- auto creation of the dummy host if it does not exist yet
- one logical webhook destination behind the shared `/webhook` URL

If you want two separate destinations, add another block:

```env
IAF_TARGET_HOME_WARNING_SOURCE=home-warning
IAF_TARGET_HOME_WARNING_HOST_NAME=home-warning
IAF_TARGET_HOME_WARNING_HOST_DISPLAY=Home Warning Alerts
IAF_TARGET_HOME_WARNING_API_KEYS=replace-with-second-long-random-key
IAF_TARGET_HOME_WARNING_NOTIFICATION_GROUPS=mail-admins
IAF_TARGET_HOME_WARNING_NOTIFICATION_SERVICE_STATES=warning,critical
IAF_TARGET_HOME_WARNING_NOTIFICATION_HOST_STATES=down
```

That gives you two logical webhook destinations on the same `/webhook` endpoint.

Example:

<!-- LANG: consistent arrow style -->
- `/webhook` + first key → `home-critical`
- `/webhook` + second key → `home-warning`

## Run It With Docker

Build the image:

```bash
docker build -t icinga-alert-forge .
```

Run it:

```bash
docker run -d \
  --name icinga-alert-forge \
  --restart unless-stopped \
  -p 8080:8080 \
  --env-file .env \
  -v iaf-logs:/var/log/webhook-bridge \
  icinga-alert-forge
```

Check logs:

```bash
docker logs -f icinga-alert-forge
```

What you want to see:

- the bridge starts without config errors
- it connects to Icinga
- it either finds or creates the configured dummy host

## Quick Smoke Test

Check health:

```bash
curl -s http://localhost:8080/health
```

Send one alert:

```bash
curl -s -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: replace-with-long-random-key" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {
        "alertname": "FastTrackProbe",
        "severity": "critical"
      },
      "annotations": {
        "summary": "Fast track deployment probe"
      }
    }]
  }'
```

If the bridge is working, you should see:

- HTTP `200`
- `host = "home-critical"` in the response
- a service named `FastTrackProbe` in Icinga

## Grafana Contact Point

Create a webhook contact point in Grafana that sends to:

```text
http://your-bridge-host:8080/webhook
```

Use:

- method: `POST`
- auth scheme: `ApiKey`
- credentials: the value from `IAF_TARGET_HOME_CRITICAL_API_KEYS`

If you have several teams or several dummy hosts, give each Grafana contact point its own key.

## First Icinga Rule

The bridge writes notification metadata to the host. Your Icinga notification rules need to read it.

Simple example:

```icinga2
apply Notification "sms-service" to Service {
  import "sms-service-notification"

  if (host.vars.notification.user_groups) {
    user_groups = host.vars.notification.user_groups
  }

  assign where host.vars.notification && host.vars.notification.user_groups
}
```

If you already have your own SMS or mail scripts, keep those. The important part is that the rule reads the host variables written by the bridge.

## After The First Successful Alert

Once the smoke test works, the usual next steps are:

1. add a second host block if you want separate routing
2. tighten API keys and admin credentials
3. put the bridge behind your normal reverse proxy if needed
4. connect the beauty panel and admin view to your ops workflow
5. move on to [Configuration](configuration.md) for the full model

## Common Fast Track Mistakes

- using the wrong Icinga API URL or wrong port
<!-- LANG: clarified permission name -->
- forgetting the `objects/create/Host` permission when auto creation is enabled
- using the same API key for several host blocks
- assuming Grafana chooses the target host from the payload
- expecting notifications to work before Icinga apply rules read `host.vars.notification.*`

## Related Docs

- [Configuration](configuration.md)
- [Usage and API](usage-and-api.md)
- [Icinga Integration](icinga-integration.md)
- [Test Environment](test-environment.md)
