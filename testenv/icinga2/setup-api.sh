#!/bin/bash
API_ENABLED="/data/etc/icinga2/features-enabled/api.conf"
API_AVAILABLE="/data/etc/icinga2/features-available/api.conf"

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
