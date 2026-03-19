#!/bin/bash
# Initialize databases before handing off to the original entrypoint

echo "Waiting for MariaDB..."
for i in $(seq 1 60); do
    if mysql -h mariadb -u icingaweb2 -picingaweb2pass icingaweb2 -e "SELECT 1" &>/dev/null; then
        echo "MariaDB is ready!"
        break
    fi
    sleep 2
done

# Import Director schema if not already done
if ! mysql -h mariadb -u director -pdirectorpass director -e "SELECT 1 FROM director_setting LIMIT 1" &>/dev/null; then
    echo "Importing Director schema..."
    DIRECTOR_SCHEMA=$(find /usr/share/icingaweb2/modules/director -name "mysql.sql" -path "*/schema/*" 2>/dev/null | head -1)
    if [ -f "$DIRECTOR_SCHEMA" ]; then
        mysql -h mariadb -u director -pdirectorpass director < "$DIRECTOR_SCHEMA"
        echo "Director schema imported."
    else
        echo "WARNING: Director schema not found"
    fi
else
    echo "Director schema already exists."
fi

# Ensure admin user in background (icingaweb_user table is created by the entrypoint)
(
    ADMIN_HASH='$2y$10$MRmtGRQMnEJOT8JRDaOvJOAfDAkXCnGJ0csLgnLOIjLGJcTlBbwpO'
    for i in $(seq 1 30); do
        if mysql -h mariadb -u icingaweb2 -picingaweb2pass icingaweb2 -e \
            "INSERT IGNORE INTO icingaweb_user (name, active, password_hash) VALUES ('admin', 1, '${ADMIN_HASH}');" &>/dev/null; then
            echo "Admin user ensured (admin/admin)"
            break
        fi
        sleep 2
    done
) &

# Hand off to the original entrypoint
exec /entrypoint "$@"
