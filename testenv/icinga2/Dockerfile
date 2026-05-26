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

# Enable IDO MySQL feature. Remove stale api.conf symlink (ApiListener is
# defined in conf.d/api.conf instead — the base-image symlink points to a
# non-existent /data-init path).
RUN mkdir -p /data/etc/icinga2/features-enabled && \
    ln -sf ../features-available/ido-mysql.conf \
           /data/etc/icinga2/features-enabled/ido-mysql.conf && \
    rm -f /data/etc/icinga2/features-enabled/api.conf

# Certs are generated at container start by entrypoint-wrapper.sh because
# /data is a VOLUME in the base image — RUN writes do not survive runtime.

COPY entrypoint-wrapper.sh /entrypoint-wrapper.sh
RUN chmod +x /entrypoint-wrapper.sh

USER icinga

ENTRYPOINT ["/entrypoint-wrapper.sh"]
CMD ["icinga2", "daemon"]
