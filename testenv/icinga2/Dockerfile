FROM icinga/icinga2:latest

USER root

# Copy custom configuration into the init directory
COPY conf.d/api-users.conf /data-init/etc/icinga2/conf.d/api-users.conf
COPY conf.d/hosts.conf /data-init/etc/icinga2/conf.d/hosts.conf

# Enable API feature
RUN ln -sf /data-init/etc/icinga2/features-available/api.conf \
           /data-init/etc/icinga2/features-enabled/api.conf

# Generate CA and node certificates manually for the API to work
RUN mkdir -p /data-init/var/lib/icinga2/certs /data-init/var/lib/icinga2/ca && \
    # Generate CA
    openssl genrsa -out /data-init/var/lib/icinga2/ca/ca.key 4096 && \
    openssl req -x509 -new -nodes -key /data-init/var/lib/icinga2/ca/ca.key \
      -sha256 -days 3650 -out /data-init/var/lib/icinga2/ca/ca.crt \
      -subj "/CN=Icinga CA" && \
    # Generate node certificate
    openssl genrsa -out /data-init/var/lib/icinga2/certs/icinga2.key 4096 && \
    openssl req -new -key /data-init/var/lib/icinga2/certs/icinga2.key \
      -out /tmp/icinga2.csr -subj "/CN=icinga2" && \
    openssl x509 -req -in /tmp/icinga2.csr \
      -CA /data-init/var/lib/icinga2/ca/ca.crt \
      -CAkey /data-init/var/lib/icinga2/ca/ca.key \
      -CAcreateserial -out /data-init/var/lib/icinga2/certs/icinga2.crt \
      -days 3650 -sha256 && \
    # Copy CA cert to certs dir (Icinga2 needs it there)
    cp /data-init/var/lib/icinga2/ca/ca.crt /data-init/var/lib/icinga2/certs/ca.crt && \
    # Fix permissions
    chown -R icinga:icinga /data-init/var/lib/icinga2/certs /data-init/var/lib/icinga2/ca && \
    rm -f /tmp/icinga2.csr

USER icinga
