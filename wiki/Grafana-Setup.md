# Grafana Integration Setup

This guide walks you through connecting Grafana Unified Alerting to IcingaAlertForge to turn your dashboard alerts into Icinga2 services.

## Prerequisites
- IcingaAlertForge is installed and reachable from your Grafana server.
- You have administrator access to the IcingaAlertForge Beauty Panel.

## Step 1: Get an API Key

Every source (like a Grafana instance or a specific team) needs an API key to authenticate against the bridge. There are two ways to get one depending on how you configured the bridge.

### Option A: Environment variable mode (default)

The API key is the value you set in `IAF_TARGET_<ID>_API_KEYS` in your `.env` or `docker-compose.yml`. Use any of those values directly in Grafana.

Example:
```env
IAF_TARGET_TEAM_A_API_KEYS=my-long-random-key-here
```

The key `my-long-random-key-here` is what you put in the Grafana contact point.

If you need to add or rotate keys, edit the env file and restart the bridge.

### Option B: Dashboard config mode (`CONFIG_IN_DASHBOARD=true`)

1.  Log in to the **IcingaAlertForge Beauty Panel** (`http://iaf-server:8080/status/beauty?admin=1`).
2.  Go to the **Settings** tab.
3.  Under **Target Configuration**, either use an existing target or click "Add Target".
4.  Find your target and click **Generate Key**.
5.  **Copy the key immediately.** For security, it will only be shown in cleartext once.

## Step 2: Add a Contact Point in Grafana
1.  In Grafana, go to **Alerting** -> **Contact points**.
2.  Click **+ Add contact point**.
3.  **Name:** `IcingaAlertForge - <Team Name>`
4.  **Integration:** Select `Webhook`.
5.  **URL:** `http://<iaf-host>:8080/webhook`
6.  **Method:** `POST`
7.  **HTTP Headers:**
    - Click **+ Add header**.
    - **Key:** `X-API-Key`
    - **Value:** `<your-copied-api-key>`
8.  (Optional) If you prefer using Authorization headers:
    - **Key:** `Authorization`
    - **Value:** `ApiKey <your-copied-api-key>`

## Step 3: Configure Notification Policy
1.  Go to **Alerting** -> **Notification policies**.
2.  Create a new policy or edit an existing one to route specific alerts (based on labels like `team=infra` or `severity=critical`) to the contact point you just created.

## Step 4: Test the Integration
1.  Return to your Contact Point in Grafana.
2.  Click the **Test** button.
3.  Click **Send test notification**.
4.  Open the **IcingaAlertForge Beauty Panel**.
5.  You should see the test alert appear in the **Real-time Webhook Flow** and the **Alert History** table.
6.  Check your Icinga2 UI; a new service named `TestAlert` should have been created on the target dummy host.

---

## Alert Mapping Strategy

IcingaAlertForge uses the following conventions to map Grafana alerts to Icinga2:

- **Service Name:** Taken from the `alertname` label.
- **Service Display Name:** Prefixed with the alert name and followed by the `summary` annotation.
- **Icinga2 State:**
    - `firing` + severity `critical` -> `CRITICAL (2)`
    - `firing` + severity `warning` -> `WARNING (1)`
    - `resolved` -> `OK (0)`
- **Notes & Links:** If your Grafana alert includes a `runbook_url` or `dashboard_url` annotation, IcingaAlertForge will automatically populate the `notes_url` in Icinga2.

## Troubleshooting

### Common Issues
1.  **401 Unauthorized:** The `X-API-Key` is missing or incorrect in the Grafana Contact Point headers.
2.  **502 Bad Gateway:** IcingaAlertForge cannot reach the Icinga2 API. Check the bridge logs and verify network connectivity to port 5665.
3.  **Service not created:**
    - Verify the API key is mapped to the correct Target Host.
    - Check if the target host actually exists in Icinga2.
    - Ensure `ICINGA2_HOST_AUTO_CREATE=true` is set if you want the bridge to handle host creation.
4.  **Context Deadline Exceeded:** The Icinga2 API is responding too slowly. Check Icinga2 CPU usage or database health.
