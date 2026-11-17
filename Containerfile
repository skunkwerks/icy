# FreeBSD container image for icy, built with podman on FreeBSD.
# See `make oci`. The Linux image for goreleaser is in Dockerfile.

ARG FREEBSD=15.1

FROM ghcr.io/freebsd/freebsd-notoolchain:${FREEBSD} AS builder

ENV ASSUME_ALWAYS_YES=yes
ENV IGNORE_OSVERSION=yes
ENV HOME=/root
ENV GOTOOLCHAIN=local
ENV CGO_ENABLED=0

COPY <<-EOF /usr/local/etc/pkg/repos/FreeBSD.conf
	FreeBSD-ports: { url: "pkg+https://pkg.FreeBSD.org/\${ABI}/latest" }
	FreeBSD-base:  { url: "pkg+https://pkg.FreeBSD.org/\${ABI}/base_release_\${VERSION_MINOR}", enabled: yes }
	FreeBSD-ports-kmods: { enabled: no }
EOF

RUN pkg update -f
RUN pkg install -qy lang/go security/ca_root_nss

# bind-mount target directories must already exist in image
RUN mkdir -p /root/go/pkg/mod /root/.cache/go-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Embed the git version the image was built from. The build args are
# declared here, not at the top, because a RUN after an ARG is rebuilt
# whenever its value changes: up there every new version would throw away
# the cached pkg install and go mod download layers.
ARG GIT_VERSION
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown

RUN test -n "${GIT_VERSION}" || \
	{ echo "GIT_VERSION is not set: use make oci, or pass --build-arg GIT_VERSION=..." >&2; exit 1; }
RUN go build -trimpath -buildvcs=false \
	-ldflags "-s -w -X main.Version=${GIT_VERSION} -X main.CommitSHA=${GIT_COMMIT} -X main.CommitDate=${BUILD_DATE}" \
	-o /out/icy ./cmd/icy
RUN /out/icy --version

FROM ghcr.io/freebsd/freebsd-notoolchain:${FREEBSD} AS runner

ENV ASSUME_ALWAYS_YES=yes
ENV IGNORE_OSVERSION=yes

COPY <<-EOF /usr/local/etc/pkg/repos/FreeBSD.conf
	FreeBSD-ports: { url: "pkg+https://pkg.FreeBSD.org/\${ABI}/latest" }
	FreeBSD-base:  { url: "pkg+https://pkg.FreeBSD.org/\${ABI}/base_release_\${VERSION_MINOR}", enabled: yes }
	FreeBSD-ports-kmods: { enabled: no }
EOF

RUN pkg update -f
RUN pkg upgrade -qy
# /var/cache/pkg is a shared host cache mounted by make oci, so never
# run pkg clean here: it would empty the host cache, and the mount is
# not part of the image anyway.
RUN pkg install -qy devel/git@tiny security/ca_root_nss shells/bash

ARG GIT_VERSION
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="icy"
LABEL org.opencontainers.image.description="A self-hostable Git server for the command line"
LABEL org.opencontainers.image.source="https://github.com/skunkwerks/icy"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.version="${GIT_VERSION}"
LABEL org.opencontainers.image.revision="${GIT_COMMIT}"
LABEL org.opencontainers.image.created="${BUILD_DATE}"

ENV ICY_DATA_PATH=/icy
ENV HOME=/icy
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8

COPY --from=builder /out/icy /usr/local/bin/icy

# icy runs as www (uid 80). A host directory mounted on /icy must be
# owned by uid 80 too.
RUN mkdir -p /icy && chown www:www /icy
WORKDIR /icy
VOLUME /icy
USER www

# ssh, http, stats, git daemon
EXPOSE 23231/tcp 23232/tcp 23233/tcp 9418/tcp

ENTRYPOINT ["/usr/local/bin/icy", "serve"]
