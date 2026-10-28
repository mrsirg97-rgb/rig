FROM alpine:3.23
RUN apk add --no-cache iptables
COPY --chmod=755 egress.sh /usr/local/bin/egress.sh
ENTRYPOINT ["/usr/local/bin/egress.sh"]
CMD ["apply"]
