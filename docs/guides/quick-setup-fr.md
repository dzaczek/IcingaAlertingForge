# IcingaAlertForge — Guide d'installation rapide (Quick Setup Guide)

> **Objectif** : 15 minutes pour obtenir un pont Grafana/Prometheus → Icinga2 fonctionnel.
> Vous faites tout ce qui est possible dans le Beauty Panel. Aucun bidouillage dans les fichiers après le démarrage.

---

## Étape 1 : Créer un utilisateur API dans Icinga2

Sur le serveur Icinga2, créez le fichier `/etc/icinga2/conf.d/api-users.conf` :

```bash
cat > /etc/icinga2/conf.d/api-users.conf << 'EOF'
object ApiUser "icinga-alertforge" {
  password = "Ton-Mot-de-passe-API-Min-12-Caracteres"
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

**Vérifiez si cela fonctionne :**
```bash
curl -k -u icinga-alertforge:Ton-Mot-de-passe-API-Min-12-Caracteres \
  https://ton-icinga2:5665/v1/status
```

---

## Étape 2 : Préparer `.env` — le minimum absolu

Copiez et modifiez **3 variables** (le reste se fait depuis le panel) :

```bash
# .env — seul ceci est requis pour démarrer
ICINGA2_HOST=https://ton-icinga2:5665
ICINGA2_USER=icinga-alertforge
ICINGA2_PASS=Ton-Mot-de-passe-API-Min-12-Caracteres

ADMIN_USER=admin
ADMIN_PASS=mon-mot-de-passe-admin

CONFIG_IN_DASHBOARD=true

# Optionnel : clé de chiffrement pour config.json (si non fournie, elle sera générée automatiquement)
# CONFIG_ENCRYPTION_KEY=ma-cle-secrete-de-chiffrement
```

**C'est tout.** Targets, clés API, limitation de débit (rate limiting), historique — vous configurerez tout dans le panel.

---

## Étape 3 : Démarrer le pont

```bash
# Démarrage rapide en local
go build -o webhook-bridge . && ./webhook-bridge

# Ou Docker Compose (recommandé pour la production)
docker compose up -d --build
```

**Vérifiez s'il est actif :**
```bash
curl http://localhost:8080/health
# → {"status":"ok","version":"v1.0.0-beta.163",...}
```

Lors du premier lancement, le pont effectue automatiquement les actions suivantes :
- il crée des hôtes dans Icinga2 (si `ICINGA2_HOST_AUTO_CREATE=true` — par défaut),
- il migre la configuration de `.env` vers `config.json` (chiffré en AES-256-GCM),
- à partir de ce moment, `config.json` est la source de vérité.

---

## Étape 4 : Panel — tout configurer depuis l'interface graphique

Ouvrez dans votre navigateur :

```
http://localhost:8080/status/beauty?admin=1
```

Connectez-vous (`admin` / `mon-mot-de-passe-admin`).

### 4a. Vérifier la connexion à Icinga2

Dans le menu latéral : **Settings** → section **Icinga2 API** → cliquez sur **Test Connection**.

Vous devriez voir la version d'Icinga2. Sinon — vérifiez l'hôte/utilisateur/mot de passe/TLS.

### 4b. Ajouter une target (hôte) et générer une clé API

Dans **Settings** → **Targets** → cliquez sur **Add Target** :

| Champ | Valeur | Description |
|---|---|---|
| ID | `grafana-prod` | automatique, peut être modifié |
| Host Name | `grafana-alerts` | nom de l'hôte dans Icinga2 |
| Source | `grafana` | étiquette de la source des alertes |

Après avoir enregistré, cliquez sur **Generate Key** — copiez la clé générée. **Elle ne s'affiche qu'une seule fois.**

### 4c. (Optionnel) Ajouter plus de targets

Par exemple, une target séparée pour Prometheus, une deuxième pour l'équipe dev :

| ID | Host Name | Source |
|---|---|---|
| `grafana-prod` | `grafana-alerts` | `grafana` |
| `prometheus-dev` | `prom-alerts-dev` | `prometheus` |

Chaque target obtient sa propre clé API — les alertes sont routées vers l'hôte correspondant dans Icinga2.

---

## Étape 5 : Connecter Grafana (ou Prometheus)

### 5a. Grafana — Contact Point

Dans Grafana : **Alerting** → **Contact points** → **New contact point** :

- **Integration** : Webhook
- **URL** : `http://ton-pont:8080/webhook`
- **HTTP Method** : POST
- **HTTP Header** : `X-API-Key` = `ta-cle-api-copiee`

Cliquez sur **Test** — si vous voyez `"Webhook received"` dans les journaux (logs) du pont, cela fonctionne.

### 5b. Prometheus Alertmanager — webhook_config

Dans `alertmanager.yml` :

```yaml
receivers:
  - name: 'icinga-alertforge'
    webhook_configs:
      - url: 'http://ton-pont:8080/webhook'
        http_config:
          headers:
            X-API-Key: 'ta-cle-api-copiee'
```

Le pont détecte automatiquement le format Alertmanager (via les champs `version`, `groupKey`, `receiver`) et le convertit en interne.

---

## Étape 6 : Vérifier si cela fonctionne de bout en bout

Envoyez une alerte de test manuellement :

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-API-Key: ta-cle-api-copiee" \
  -d '{
    "status": "firing",
    "alerts": [{
      "status": "firing",
      "labels": {"alertname": "Alerte Test", "severity": "critical"},
      "annotations": {"summary": "Test avec curl - ça marche !"}
    }]
  }'
```

**Réponse attendue :**
```json
{
  "request_id": "...",
  "host": "grafana-alerts",
  "results": [{
    "status": "processed",
    "service": "Alerte Test",
    "exit_status": 2,
    "label": "CRITICAL",
    "icinga_ok": true
  }]
}
```

**Dans Icinga2** vérifiez :
```bash
curl -k -u icinga-alertforge:mot-de-passe \
  https://ton-icinga2:5665/v1/objects/services/grafana-alerts!Alerte%20Test
```

---

## Ce qui se passe sous le capot (flux de données)

```
Grafana/Prometheus → webhook POST → Pont (Bridge) → Icinga2 API
                                                  → Historique (JSONL)
                                                  → SSE (dashboard en direct)
                                                  → Métriques (/metrics)
```

1. Le pont reçoit un webhook sur `/webhook` avec l'en-tête `X-API-Key`
2. La clé API est mappée sur une target → nom d'hôte dans Icinga2
3. Le pont crée un service dans Icinga2 (si c'est la première fois) en tant que vérification passive (passive check)
4. Le pont envoie un `process-check-result` avec un exit_status : 0=OK, 1=WARNING, 2=CRITICAL
5. Le résultat est enregistré dans l'historique, publié via SSE sur le dashboard, et met à jour les métriques

---

## Panel — que pouvez-vous faire d'autre ?

| Fonctionnalité | Où dans le panel | Description |
|---|---|---|
| Ajouter/supprimer des targets | Settings → Targets | Nouvel hôte dans Icinga2 + clé API |
| Générer des clés API | Settings → Targets → Generate Key | Nouvelle clé pour une target existante |
| Afficher les clés | Settings → Targets → Reveal Keys | Affiche les clés masquées |
| Changer le mot de passe admin | Settings → Admin | Rechargement à chaud (Hot-reload), sans redémarrage |
| Tester la connexion | Settings → Test Icinga2 | Vérifie si le pont voit Icinga2 |
| Sauvegarder la configuration | Settings → Export | Télécharge un fichier JSON chiffré |
| Restaurer la configuration | Settings → Import | Importe un fichier JSON de sauvegarde |
| Gérer les utilisateurs | Admin → Users | RBAC : viewer, operator, admin |
| Geler les alertes (Freeze) | Services → Freeze | Met les alertes en sourdine pendant X secondes ou indéfiniment |
| Panel Dev (débogage) | Dev → Toggle Debug | Aperçu des requêtes/réponses vers l'API Icinga2 |

---

## Dépannage (Troubleshooting)

| Problème | À vérifier |
|---|---|
| Le pont ne démarre pas | `ICINGA2_HOST` doit être joignable — vérifiez le pare-feu et TLS |
| 401 Unauthorized | La clé API ne correspond à aucune target |
| 502 Bad Gateway | Icinga2 ne répond pas — vérifiez `ICINGA2_HOST` et les identifiants |
| "Host does not exist" | Définissez `ICINGA2_HOST_AUTO_CREATE=true` ou créez l'hôte manuellement |
| Le panel ne montre pas les Settings | `CONFIG_IN_DASHBOARD=true` n'est pas défini |
| Les modifications dans le panel ne fonctionnent pas | Vérifiez les journaux (logs) — le rechargement à chaud peut échouer avec des données invalides |
