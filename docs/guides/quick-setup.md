# IcingaAlertForge — Quick Setup Guide

> **Cel**: 15 minut do działającego mostka Grafana/Prometheus → Icinga2.
> Wszystko co się da, robisz w panelu Beauty Panel. Zero grzebania w plikach po starcie.

---

## Krok 1: Utwórz API usera w Icinga2

Na serwerze Icinga2 utwórz plik `/etc/icinga2/conf.d/api-users.conf`:

```bash
cat > /etc/icinga2/conf.d/api-users.conf << 'EOF'
object ApiUser "icinga-alertforge" {
  password = "Twoje-Haslo-API-Min-12-Znakow"
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

**Sprawdz czy dziala:**
```bash
curl -k -u icinga-alertforge:Twoje-Haslo-API-Min-12-Znakow \
  https://twoj-icinga2:5665/v1/status
```

---

## Krok 2: Przygotuj `.env` — absolutne minimum

Skopiuj i edytuj **3 zmienne** (reszta z panelu):

```bash
# .env — tylko to jest wymagane na start
ICINGA2_HOST=https://twoj-icinga2:5665
ICINGA2_USER=icinga-alertforge
ICINGA2_PASS=Twoje-Haslo-API-Min-12-Znakow

ADMIN_USER=admin
ADMIN_PASS=moje-admin-haslo

CONFIG_IN_DASHBOARD=true

# Opcjonalnie: klucz szyfrujący do config.json (jak nie podasz, sam wygeneruje)
# CONFIG_ENCRYPTION_KEY=moj-tajny-klucz-szyfrujacy
```

**To wszystko.** Targety, API keye, rate limiting, historię — wszystko ustawisz w panelu.

---

## Krok 3: Uruchom bridge

```bash
# Szybki start lokalnie
go build -o webhook-bridge . && ./webhook-bridge

# Albo Docker Compose (zalecane na produkcje)
docker compose up -d --build
```

**Sprawdz czy zyje:**
```bash
curl http://localhost:8080/health
# → {"status":"ok","version":"v1.0.0-beta.163",...}
```

Przy pierwszym uruchomieniu bridge automatycznie:
- tworzy hosty w Icinga2 (jeśli `ICINGA2_HOST_AUTO_CREATE=true` — domyślnie),
- migruje konfigurację z `.env` do `config.json` (szyfrowane AES-256-GCM),
- od tego momentu `config.json` jest źródłem prawdy.

---

## Krok 4: Panel — konfigurujesz wszystko z GUI

Otwórz w przeglądarce:

```
http://localhost:8080/status/beauty?admin=1
```

Zaloguj się (`admin` / `moje-admin-haslo`).

### 4a. Sprawdź połączenie z Icinga2

W bocznym menu: **Settings** → sekcja **Icinga2 API** → kliknij **Test Connection**.

Powinieneś zobaczyć wersję Icinga2. Jeśli nie — sprawdź host/user/pass/TLS.

### 4b. Dodaj target (host) i wygeneruj API key

W **Settings** → **Targets** → kliknij **Add Target**:

| Pole | Wartość | Opis |
|---|---|---|
| ID | `grafana-prod` | automatycznie, można zmienić |
| Host Name | `grafana-alerts` | nazwa hosta w Icinga2 |
| Source | `grafana` | etykieta źródła alertów |

Po zapisaniu kliknij **Generate Key** — skopiuj wygenerowany klucz. **Pokazuje się tylko raz.**

### 4c. (Opcjonalnie) Dodaj więcej targetów

Np. osobny target dla Prometheusa, drugi dla zespołu dev:

| ID | Host Name | Source |
|---|---|---|
| `grafana-prod` | `grafana-alerts` | `grafana` |
| `prometheus-dev` | `prom-alerts-dev` | `prometheus` |

Każdy target dostaje własny klucz API — alerty routują się na odpowiedni host w Icinga2.

---

## Krok 5: Podłącz Grafana (lub Prometheus)

### 5a. Grafana — Contact Point

W Grafanie: **Alerting** → **Contact points** → **New contact point**:

- **Integration**: Webhook
- **URL**: `http://twoj-bridge:8080/webhook`
- **HTTP Method**: POST
- **HTTP Header**: `X-API-Key` = `twoj-skopiowany-klucz-api`

Kliknij **Test** — jeśli w logach bridge'a widzisz `"Webhook received"`, działa.

### 5b. Prometheus Alertmanager — webhook_config

W `alertmanager.yml`:

```yaml
receivers:
  - name: 'icinga-alertforge'
    webhook_configs:
      - url: 'http://twoj-bridge:8080/webhook'
        http_config:
          headers:
            X-API-Key: 'twoj-skopiowany-klucz-api'
```

Bridge automatycznie wykrywa format Alertmanagera (po polach `version`, `groupKey`, `receiver`) i konwertuje go wewnętrznie.

---

## Krok 6: Sprawdź czy działa end-to-end

Wyślij testowy alert ręcznie:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: twoj-skopiowany-klucz-api" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "Test Alert", "severity": "critical"},
      "annotations": {"summary": "Test z curl - dziala!"}
    }]
  }'
```

**Oczekiwana odpowiedź:**
```json
{
  "request_id": "...",
  "host": "grafana-alerts",
  "results": [{
    "status": "processed",
    "service": "Test Alert",
    "exit_status": 2,
    "label": "CRITICAL",
    "icinga_ok": true
  }]
}
```

**W Icinga2** sprawdź:
```bash
curl -k -u icinga-alertforge:haslo \
  https://twoj-icinga2:5665/v1/objects/services/grafana-alerts!Test%20Alert
```

---

## Co się dzieje pod spodem (przepływ danych)

```
Grafana/Prometheus → webhook POST → Bridge → Icinga2 API
                                            → History (JSONL)
                                            → SSE (dashboard na żywo)
                                            → Metrics (/metrics)
```

1. Bridge odbiera webhook na `/webhook` z nagłówkiem `X-API-Key`
2. Klucz API mapuje się na target → nazwa hosta w Icinga2
3. Bridge tworzy service w Icinga2 (jeśli pierwszy raz) jako passive check
4. Bridge wysyła `process-check-result` z exit_status: 0=OK, 1=WARNING, 2=CRITICAL
5. Wynik zapisuje się w historii, publikuje SSE do dashboardu, aktualizuje metryki

---

## Panel — co jeszcze możesz zrobić

| Funkcja | Gdzie w panelu | Opis |
|---|---|---|
| Dodaj/usuń targety | Settings → Targets | Nowy host w Icinga2 + klucz API |
| Generuj klucze API | Settings → Targets → Generate Key | Nowy klucz do istniejącego targetu |
| Podejrzyj klucze | Settings → Targets → Reveal Keys | Pokazuje zamaskowane klucze |
| Zmień hasło admina | Settings → Admin | Hot-reload, bez restartu |
| Testuj połączenie | Settings → Test Icinga2 | Sprawdza czy bridge widzi Icinga2 |
| Backup konfiguracji | Settings → Export | Pobiera zaszyfrowany JSON |
| Przywróć konfigurację | Settings → Import | Wgrywa backup JSON |
| Zarządzaj użytkownikami | Admin → Users | RBAC: viewer, operator, admin |
| Zamrażaj alerty | Services → Freeze | Wycisza alerty na X sekund lub permanentnie |
| Dev panel (debug) | Dev → Toggle Debug | Podgląd requestów/response'ów do Icinga2 API |

---

## Troubleshooting

| Problem | Sprawdź |
|---|---|
| Bridge nie startuje | `ICINGA2_HOST` musi być osiągalny — sprawdź firewalla i TLS |
| 401 Unauthorized | Klucz API nie pasuje do żadnego targetu |
| 502 Bad Gateway | Icinga2 nieodpowiada — sprawdź `ICINGA2_HOST` i credentials |
| "Host does not exist" | Ustaw `ICINGA2_HOST_AUTO_CREATE=true` albo stwórz hosta ręcznie |
| Panel nie pokazuje Settings | `CONFIG_IN_DASHBOARD=true` nie jest ustawione |
| Zmiany w panelu nie działają | Sprawdź logi — hot-reload może failować przy błędnych danych |
