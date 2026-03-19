FROM icinga/icingaweb2:latest

USER root

# Only need mariadb client for the DB init script
RUN apt-get update && apt-get install -y --no-install-recommends \
    mariadb-client \
    && rm -rf /var/lib/apt/lists/*

# Custom entrypoint wrapper to init DBs before the real entrypoint
COPY entrypoint-wrapper.sh /entrypoint-wrapper.sh
RUN chmod +x /entrypoint-wrapper.sh

USER www-data

ENTRYPOINT ["/entrypoint-wrapper.sh"]
CMD ["bash", "-eo", "pipefail", "-c", ". /etc/apache2/envvars; exec apache2 -DFOREGROUND"]
