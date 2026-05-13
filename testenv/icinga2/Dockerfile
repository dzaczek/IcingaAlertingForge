FROM icinga/icinga2:latest

USER root

# IDO MySQL module is already built into the base image (libmysql_shim.so)
# We only need the mariadb client for the init script
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    mariadb-client \
    && rm -rf /var/lib/apt/lists/*

# Copy custom configuration into the init directory.
# api.conf defines ApiListener directly in conf.d (bypasses features-enabled).
COPY conf.d/api.conf           /data-init/etc/icinga2/conf.d/api.conf
COPY conf.d/api-users.conf     /data-init/etc/icinga2/conf.d/api-users.conf
COPY conf.d/hosts.conf         /data-init/etc/icinga2/conf.d/hosts.conf
COPY conf.d/users.conf         /data-init/etc/icinga2/conf.d/users.conf
COPY conf.d/notifications.conf /data-init/etc/icinga2/conf.d/notifications.conf
COPY conf.d/ido-mysql.conf     /data-init/etc/icinga2/features-available/ido-mysql.conf

# Enable IDO MySQL feature using a relative symlink
RUN mkdir -p /data-init/etc/icinga2/features-enabled && \
    ln -sf ../features-available/ido-mysql.conf \
           /data-init/etc/icinga2/features-enabled/ido-mysql.conf

# Generate CA and node certificates manually for the API to work
RUN mkdir -p /data-init/var/lib/icinga2/certs /data-init/var/lib/icinga2/ca && \
    openssl genrsa -out /data-init/var/lib/icinga2/ca/ca.key 4096 && \
    openssl req -x509 -new -nodes -key /data-init/var/lib/icinga2/ca/ca.key \
      -sha256 -days 3650 -out /data-init/var/lib/icinga2/ca/ca.crt \
      -subj "/CN=Icinga CA" && \
    openssl genrsa -out /data-init/var/lib/icinga2/certs/icinga2.key 4096 && \
    openssl req -new -key /data-init/var/lib/icinga2/certs/icinga2.key \
      -out /tmp/icinga2.csr -subj "/CN=icinga2" && \
    openssl x509 -req -in /tmp/icinga2.csr \
      -CA /data-init/var/lib/icinga2/ca/ca.crt \
      -CAkey /data-init/var/lib/icinga2/ca/ca.key \
      -CAcreateserial -out /data-init/var/lib/icinga2/certs/icinga2.crt \
      -days 3650 -sha256 && \
    cp /data-init/var/lib/icinga2/ca/ca.crt /data-init/var/lib/icinga2/certs/ca.crt && \
    chown -R icinga:icinga /data-init/var/lib/icinga2/certs /data-init/var/lib/icinga2/ca && \
    rm -f /tmp/icinga2.csr

# Custom entrypoint wrapper to import IDO schema before starting Icinga2
COPY entrypoint-wrapper.sh /entrypoint-wrapper.sh
RUN chmod +x /entrypoint-wrapper.sh

USER icinga

ENTRYPOINT ["/entrypoint-wrapper.sh"]
CMD ["icinga2", "daemon"]
