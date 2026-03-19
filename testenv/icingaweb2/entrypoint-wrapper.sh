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

# Import Director schema if not already done (entrypoint does NOT handle this)
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

# Create admin user in background via PHP (avoids bash escaping issues with bcrypt hash)
# PHP handles password_hash + PDO insert with proper escaping
nohup php /create-admin.php > /tmp/admin-init.log 2>&1 &

# Hand off to the original entrypoint
exec /entrypoint "$@"
