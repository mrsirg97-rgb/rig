FROM alpine:3.23
# 1.11.3-r0 is what Alpine 3.23 ships (measured 2026-10-26, apk search finds no
# other build). CVE-2026-54387 and CVE-2026-54388 (HTTP request smuggling) and
# CVE-2026-55202 (stathost Host-header validation) reach through 1.11.3: there is
# no patched Alpine build to move to yet. What keeps this deployment out of their
# way is the config, not the version: the smuggling pair needs a client that is
# already inside the Allow line AND past BasicAuth, and stathost is never enabled.
RUN apk add --no-cache tinyproxy=1.11.3-r0
# The conf and the deny-list are baked, not mounted, so `docker compose up -d
# --build` is the only path that applies an edit: a plain restart keeps the copy
# already in the image. --chown is load-bearing: the container runs as uid 100
# (tinyproxy) and the host conf is 0600 because it holds the BasicAuth password,
# so a root-owned 0600 copy is unreadable -> "Could not open config file", exit
# 70, a restart loop that docker ps still reports as Up (measured 2026-10-26).
COPY --chown=tinyproxy:tinyproxy --chmod=600 tinyproxy.conf /etc/tinyproxy/tinyproxy.conf
COPY filter /etc/tinyproxy/filter
USER tinyproxy
EXPOSE 8888
CMD ["tinyproxy", "-d", "-c", "/etc/tinyproxy/tinyproxy.conf"]
