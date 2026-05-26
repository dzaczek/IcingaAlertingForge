#!/bin/bash
API_ENABLED="/data/etc/icinga2/features-enabled/api.conf"
API_AVAILABLE="/data/etc/icinga2/features-available/api.conf"

rm -f /data/etc/icinga2/conf.d/api.conf

cat << 'INNER_EOF' > "$API_AVAILABLE"
object ApiListener "api" {
  cert_path = "/data/var/lib/icinga2/certs/icinga2.crt"
  key_path  = "/data/var/lib/icinga2/certs/icinga2.key"
  ca_path   = "/data/var/lib/icinga2/certs/ca.crt"

  bind_port      = 5665
  accept_config  = true
  accept_commands = true
}
INNER_EOF

if [ ! -L "$API_ENABLED" ] && [ ! -f "$API_ENABLED" ]; then
    echo "[setup-api] Enabling API feature..."
    ln -sf "$API_AVAILABLE" "$API_ENABLED"
    echo "[setup-api] API feature enabled"
else
    echo "[setup-api] API feature already enabled"
fi
