#!/bin/bash
# Enable the Icinga2 API feature by creating the symlink
# This runs as a docker-entrypoint.d script before Icinga2 starts

API_ENABLED="/data/etc/icinga2/features-enabled/api.conf"
API_AVAILABLE="/data/etc/icinga2/features-available/api.conf"

if [ ! -L "$API_ENABLED" ] && [ ! -f "$API_ENABLED" ]; then
    echo "[setup-api] Enabling API feature..."
    ln -sf "$API_AVAILABLE" "$API_ENABLED"
    echo "[setup-api] API feature enabled"
else
    echo "[setup-api] API feature already enabled"
fi
