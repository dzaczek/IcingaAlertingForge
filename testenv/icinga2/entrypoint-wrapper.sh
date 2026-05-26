#!/bin/sh
# Wait for MariaDB, import IDO schema, generate TLS certs, start icinga2.

CERT_DIR="/data/var/lib/icinga2/certs"
CA_DIR="/data/var/lib/icinga2/ca"

# Generate TLS certs at runtime (not during build — /data is a VOLUME)
if [ ! -f "$CERT_DIR/icinga2.crt" ] || [ ! -f "$CERT_DIR/icinga2.key" ]; then
    echo "Generating Icinga2 TLS certificates..."
    mkdir -p "$CERT_DIR" "$CA_DIR"
    openssl genrsa -out "$CA_DIR/ca.key" 4096
    openssl req -x509 -new -nodes -key "$CA_DIR/ca.key" \
        -sha256 -days 3650 -out "$CA_DIR/ca.crt" \
        -subj "/CN=Icinga CA"
    openssl genrsa -out "$CERT_DIR/icinga2.key" 4096
    openssl req -new -key "$CERT_DIR/icinga2.key" \
        -out /tmp/icinga2.csr -subj "/CN=icinga2"
    openssl x509 -req -in /tmp/icinga2.csr \
        -CA "$CA_DIR/ca.crt" -CAkey "$CA_DIR/ca.key" \
        -CAcreateserial -out "$CERT_DIR/icinga2.crt" \
        -days 3650 -sha256
    cp "$CA_DIR/ca.crt" "$CERT_DIR/ca.crt"
    chown -R icinga:icinga "$CERT_DIR" "$CA_DIR"
    rm -f /tmp/icinga2.csr
    echo "TLS certificates generated."
fi

MYSQL_OPTS="-h mariadb -u icinga2_ido -picinga2_idopass --ssl=FALSE icinga2_ido"

# Wait for MariaDB and import IDO schema if needed
echo "Waiting for MariaDB..."
for i in $(seq 1 60); do
    if mysql $MYSQL_OPTS -e "SELECT 1" >/dev/null 2>&1; then
        echo "MariaDB is ready!"
        break
    fi
    sleep 2
done

if ! mysql $MYSQL_OPTS -e "SELECT 1 FROM icinga_dbversion LIMIT 1" >/dev/null 2>&1; then
    echo "Importing IDO MySQL schema..."
    SCHEMA_FILE=$(find /usr/share/icinga2-ido-mysql -name "mysql.sql" 2>/dev/null | head -1)
    if [ -f "$SCHEMA_FILE" ]; then
        mysql $MYSQL_OPTS < "$SCHEMA_FILE" && echo "IDO schema imported." || echo "ERROR: schema import failed!"
    else
        echo "ERROR: IDO schema file not found!"
    fi
else
    echo "IDO schema already exists."
fi

exec "$@"
