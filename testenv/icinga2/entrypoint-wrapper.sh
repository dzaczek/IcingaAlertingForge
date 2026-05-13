#!/bin/sh
# Run custom init scripts before handing off to icinga2 daemon

# Initialize /data from /data-init on first start (replaces the base image entrypoint logic)
if [ -d /data-init ] && [ ! -f /data/etc/icinga2/icinga2.conf ]; then
    echo "Initializing /data from /data-init..."
    cp -a /data-init/. /data/
    echo "Data initialized."
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
