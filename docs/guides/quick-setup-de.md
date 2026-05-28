# IcingaAlertForge — Quick Setup Guide

> **Ziel**: 15 Minuten bis zu einer funktionierenden Grafana/Prometheus → Icinga2 Bridge.
> Alles was möglich ist, erledigen Sie im Beauty Panel. Kein Herumstöbern in Dateien nach dem Start.

---

## Schritt 1: API-Benutzer in Icinga2 erstellen

Erstellen Sie auf dem Icinga2-Server die Datei `/etc/icinga2/conf.d/api-users.conf`:

```bash
cat > /etc/icinga2/conf.d/api-users.conf << 'EOF'
object ApiUser "icinga-alertforge" {
  password = "Dein-API-Passwort-Min-12-Zeichen"
  permissions = [
    "actions/process-check-result",
    "objects/query/Host",
    "objects/query/Service",
    "objects/create/Host",
    "objects/create/Service",
    "objects/delete/Service",
    "status/query"
  ]
}
EOF

systemctl restart icinga2
```

**Überprüfen, ob es funktioniert:**
```bash
curl -k -u icinga-alertforge:Dein-API-Passwort-Min-12-Zeichen \
  https://dein-icinga2:5665/v1/status
```

---

## Schritt 2: `.env` vorbereiten — absolutes Minimum

Kopieren und bearbeiten Sie **3 Variablen** (der Rest wird über das Panel konfiguriert):

```bash
# .env — nur dies ist zum Starten erforderlich
ICINGA2_HOST=https://dein-icinga2:5665
ICINGA2_USER=icinga-alertforge
ICINGA2_PASS=Dein-API-Passwort-Min-12-Zeichen

ADMIN_USER=admin
ADMIN_PASS=mein-admin-passwort

CONFIG_IN_DASHBOARD=true

# Optional: Verschlüsselungsschlüssel für config.json (falls nicht angegeben, wird er selbst generiert)
# CONFIG_ENCRYPTION_KEY=mein-geheimer-verschluesselungsschluessel
```

**Das ist alles.** Targets, API-Schlüssel, Rate Limiting, Verlauf — alles konfigurieren Sie im Panel.

---

## Schritt 3: Bridge starten

```bash
# Schnellstart lokal
go build -o webhook-bridge . && ./webhook-bridge

# Oder Docker Compose (empfohlen für die Produktion)
docker compose up -d --build
```

**Überprüfen, ob sie läuft:**
```bash
curl http://localhost:8080/health
# → {"status":"ok","version":"v1.0.0-beta.163",...}
```

Beim ersten Start führt die Bridge automatisch Folgendes aus:
- Erstellt Hosts in Icinga2 (falls `ICINGA2_HOST_AUTO_CREATE=true` — Standard),
- Migriert die Konfiguration von `.env` nach `config.json` (AES-256-GCM verschlüsselt),
- ab diesem Zeitpunkt ist `config.json` die Single Source of Truth.

---

## Schritt 4: Panel — alles über die GUI konfigurieren

Im Browser öffnen:

```
http://localhost:8080/status/beauty?admin=1
```

Anmelden (`admin` / `mein-admin-passwort`).

### 4a. Verbindung zu Icinga2 überprüfen

Im Seitenmenü: **Settings** → Abschnitt **Icinga2 API** → klicken Sie auf **Test Connection**.

Sie sollten die Icinga2-Version sehen. Wenn nicht — überprüfen Sie Host/Benutzer/Passwort/TLS.

### 4b. Ein Target (Host) hinzufügen und einen API-Schlüssel generieren

Unter **Settings** → **Targets** → klicken Sie auf **Add Target**:

| Feld | Wert | Beschreibung |
|---|---|---|
| ID | `grafana-prod` | automatisch, kann geändert werden |
| Host Name | `grafana-alerts` | Hostname in Icinga2 |
| Source | `grafana` | Label der Alarmquelle |

Klicken Sie nach dem Speichern auf **Generate Key** — kopieren Sie den generierten Schlüssel. **Er wird nur einmal angezeigt.**

### 4c. (Optional) Weitere Targets hinzufügen

Z.B. ein separates Target für Prometheus, ein zweites für das Dev-Team:

| ID | Host Name | Source |
|---|---|---|
| `grafana-prod` | `grafana-alerts` | `grafana` |
| `prometheus-dev` | `prom-alerts-dev` | `prometheus` |

Jedes Target erhält einen eigenen API-Schlüssel — Alarme werden an den entsprechenden Host in Icinga2 weitergeleitet.

---

## Schritt 5: Grafana (oder Prometheus) verbinden

### 5a. Grafana — Contact Point

In Grafana: **Alerting** → **Contact points** → **New contact point**:

- **Integration**: Webhook
- **URL**: `http://deine-bridge:8080/webhook`
- **HTTP Method**: POST
- **HTTP Header**: `X-API-Key` = `dein-kopierter-api-schluessel`

Klicken Sie auf **Test** — wenn Sie `"Webhook received"` in den Logs der Bridge sehen, funktioniert es.

### 5b. Prometheus Alertmanager — webhook_config

In der `alertmanager.yml`:

```yaml
receivers:
  - name: 'icinga-alertforge'
    webhook_configs:
      - url: 'http://deine-bridge:8080/webhook'
        http_config:
          headers:
            X-API-Key: 'dein-kopierter-api-schluessel'
```

Die Bridge erkennt das Alertmanager-Format automatisch (anhand der Felder `version`, `groupKey`, `receiver`) und konvertiert es intern.

---

## Schritt 6: Überprüfen, ob es End-to-End funktioniert

Senden Sie manuell einen Testalarm:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dein-kopierter-api-schluessel" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "Test Alarm", "severity": "critical"},
      "annotations": {"summary": "Test mit curl - funktioniert!"}
    }]
  }'
```

**Erwartete Antwort:**
```json
{
  "request_id": "...",
  "host": "grafana-alerts",
  "results": [{
    "status": "processed",
    "service": "Test Alarm",
    "exit_status": 2,
    "label": "CRITICAL",
    "icinga_ok": true
  }]
}
```

**In Icinga2** prüfen:
```bash
curl -k -u icinga-alertforge:passwort \
  https://dein-icinga2:5665/v1/objects/services/grafana-alerts!Test%20Alarm
```

---

## Was unter der Haube passiert (Datenfluss)

```
Grafana/Prometheus → webhook POST → Bridge → Icinga2 API
                                            → History (JSONL)
                                            → SSE (Live Dashboard)
                                            → Metrics (/metrics)
```

1. Die Bridge empfängt einen Webhook unter `/webhook` mit dem Header `X-API-Key`
2. Der API-Schlüssel wird einem Target zugeordnet → Hostname in Icinga2
3. Die Bridge erstellt einen Service in Icinga2 (beim ersten Mal) als passiven Check
4. Die Bridge sendet ein `process-check-result` mit exit_status: 0=OK, 1=WARNING, 2=CRITICAL
5. Das Ergebnis wird im Verlauf gespeichert, über SSE im Dashboard veröffentlicht und aktualisiert die Metriken

---

## Panel — was Sie sonst noch tun können

| Funktion | Wo im Panel | Beschreibung |
|---|---|---|
| Targets hinzufügen/entfernen | Settings → Targets | Neuer Host in Icinga2 + API-Schlüssel |
| API-Schlüssel generieren | Settings → Targets → Generate Key | Neuer Schlüssel für ein bestehendes Target |
| Schlüssel anzeigen | Settings → Targets → Reveal Keys | Zeigt maskierte Schlüssel an |
| Admin-Passwort ändern | Settings → Admin | Hot-Reload, ohne Neustart |
| Verbindung testen | Settings → Test Icinga2 | Überprüft, ob die Bridge Icinga2 sieht |
| Konfigurations-Backup | Settings → Export | Lädt verschlüsselte JSON-Datei herunter |
| Konfiguration wiederherstellen | Settings → Import | Lädt Backup-JSON hoch |
| Benutzer verwalten | Admin → Users | RBAC: viewer, operator, admin |
| Alarme einfrieren | Services → Freeze | Schaltet Alarme für X Sekunden oder dauerhaft stumm |
| Dev Panel (Debug) | Dev → Toggle Debug | Vorschau von Requests/Responses an die Icinga2-API |

---

## Fehlerbehebung (Troubleshooting)

| Problem | Überprüfen Sie |
|---|---|
| Bridge startet nicht | `ICINGA2_HOST` muss erreichbar sein — Firewall und TLS prüfen |
| 401 Unauthorized | API-Schlüssel passt zu keinem Target |
| 502 Bad Gateway | Icinga2 antwortet nicht — `ICINGA2_HOST` und Zugangsdaten prüfen |
| "Host does not exist" | Setzen Sie `ICINGA2_HOST_AUTO_CREATE=true` oder erstellen Sie den Host manuell |
| "Import references unknown template" (500) | Fehlende `generic-host`/`generic-service`-Templates in Icinga2 — fügen Sie sie zu `/etc/icinga2/conf.d/templates.conf` hinzu oder deaktivieren Sie `ICINGA2_HOST_AUTO_CREATE=false`. Siehe [Fehlerbehebung](../troubleshooting.md#import-references-unknown-template-http-500) |
| Panel zeigt Settings nicht an | `CONFIG_IN_DASHBOARD=true` ist nicht gesetzt |
| Änderungen im Panel funktionieren nicht | Logs prüfen — Hot-Reload kann bei ungültigen Daten fehlschlagen |
