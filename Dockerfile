FROM alpine:latest

# Create directories
WORKDIR /icy
# Expose data volume
VOLUME /icy

# Environment variables
ENV ICY_DATA_PATH "/icy"
ENV ICY_INITIAL_ADMIN_KEYS ""
# workaround to prevent slowness in docker when running with a tty
ENV CI "1"

# Expose ports
# SSH
EXPOSE 23231/tcp
# HTTP
EXPOSE 23232/tcp
# Stats
EXPOSE 23233/tcp
# Git
EXPOSE 9418/tcp

# Set the default command
ENTRYPOINT [ "/usr/local/bin/icy", "serve" ]

RUN apk update && apk add --update git bash openssh && rm -rf /var/cache/apk/*

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/icy /usr/local/bin/icy
