#!/bin/bash
# Run custom init scripts before handing off to the original entrypoint

# Import IDO MySQL schema if not yet done
echo "Waiting for MariaDB..."
for i in $(seq 1 60); do
    if mysql -h mariadb -u icinga2_ido -picinga2_idopass icinga2_ido -e "SELECT 1" &>/dev/null; then
        echo "MariaDB is ready!"
        break
    fi
    sleep 2
done

if ! mysql -h mariadb -u icinga2_ido -picinga2_idopass icinga2_ido -e "SELECT 1 FROM icinga_dbversion LIMIT 1" &>/dev/null; then
    echo "Importing IDO MySQL schema..."
    SCHEMA_FILE=$(find /usr/share/icinga2-ido-mysql -name "mysql.sql" 2>/dev/null | head -1)
    if [ -f "$SCHEMA_FILE" ]; then
        mysql -h mariadb -u icinga2_ido -picinga2_idopass icinga2_ido < "$SCHEMA_FILE"
        echo "IDO schema imported."
    else
        echo "ERROR: IDO schema file not found!"
    fi
else
    echo "IDO schema already exists."
fi

# Hand off to the original entrypoint
exec /entrypoint "$@"
