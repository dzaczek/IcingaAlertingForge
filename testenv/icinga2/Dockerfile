FROM icinga/icinga2:latest

USER root

RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    mariadb-client \
    && rm -rf /var/lib/apt/lists/*

# Copy configuration directly into /data/etc/icinga2/conf.d/ so Docker's
# volume initialisation (which seeds from the container's /data filesystem)
# includes our files on first start.
COPY conf.d/api.conf           /data/etc/icinga2/conf.d/api.conf
COPY conf.d/api-users.conf     /data/etc/icinga2/conf.d/api-users.conf
COPY conf.d/hosts.conf         /data/etc/icinga2/conf.d/hosts.conf
COPY conf.d/users.conf         /data/etc/icinga2/conf.d/users.conf
COPY conf.d/notifications.conf /data/etc/icinga2/conf.d/notifications.conf
COPY conf.d/ido-mysql.conf     /data/etc/icinga2/features-available/ido-mysql.conf

# Enable IDO MySQL feature and the API feature (via symlink)
RUN mkdir -p /data/etc/icinga2/features-enabled && \
    ln -sf ../features-available/ido-mysql.conf \
           /data/etc/icinga2/features-enabled/ido-mysql.conf

# Generate TLS certs into /data/var/lib/icinga2/certs/
RUN mkdir -p /data/var/lib/icinga2/certs /data/var/lib/icinga2/ca && \
    openssl genrsa -out /data/var/lib/icinga2/ca/ca.key 4096 && \
    openssl req -x509 -new -nodes -key /data/var/lib/icinga2/ca/ca.key \
      -sha256 -days 3650 -out /data/var/lib/icinga2/ca/ca.crt \
      -subj "/CN=Icinga CA" && \
    openssl genrsa -out /data/var/lib/icinga2/certs/icinga2.key 4096 && \
    openssl req -new -key /data/var/lib/icinga2/certs/icinga2.key \
      -out /tmp/icinga2.csr -subj "/CN=icinga2" && \
    openssl x509 -req -in /tmp/icinga2.csr \
      -CA /data/var/lib/icinga2/ca/ca.crt \
      -CAkey /data/var/lib/icinga2/ca/ca.key \
      -CAcreateserial -out /data/var/lib/icinga2/certs/icinga2.crt \
      -days 3650 -sha256 && \
    cp /data/var/lib/icinga2/ca/ca.crt /data/var/lib/icinga2/certs/ca.crt && \
    chown -R icinga:icinga /data/var/lib/icinga2/certs /data/var/lib/icinga2/ca \
                           /data/etc/icinga2/conf.d /data/etc/icinga2/features-available \
                           /data/etc/icinga2/features-enabled && \
    rm -f /tmp/icinga2.csr

COPY entrypoint-wrapper.sh /entrypoint-wrapper.sh
RUN chmod +x /entrypoint-wrapper.sh

USER icinga

ENTRYPOINT ["/entrypoint-wrapper.sh"]
CMD ["icinga2", "daemon"]
