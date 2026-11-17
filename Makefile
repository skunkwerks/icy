# FreeBSD container images for icy, built with podman on FreeBSD. The
# image is native to the host CPU (amd64 or arm64). This is BSD make.
# Day-to-day Go tasks live in Taskfile.yaml.
#
#   make oci            build the image as icy:<version> and icy:latest
#   make oci-archive    save it as icy.tar, overwritten on each build
#
# podman on FreeBSD needs root, so run as root or set PODMAN:
#
#   make PODMAN="doas podman" oci

CACHE?=		/var/cache/podman
FREEBSD?=	15.1
IMAGE?=		icy
PODMAN?=	podman
GIT?=		git

VERSION!=	${GIT} describe --dirty --abbrev=7 --tags --always --first-parent
COMMIT!=	${GIT} rev-parse --short HEAD

.PHONY: cache oci oci-archive

cache: # init local cache dirs
	@mkdir -p ${CACHE}/pkg ${CACHE}/go-mod ${CACHE}/go-build

oci: cache # build a FreeBSD container image for the host CPU
	@[ "$$(uname -s)" = FreeBSD ] || \
		{ echo "make oci builds a FreeBSD image and must run on FreeBSD" >&2; exit 1; }
	${PODMAN} build \
		--build-arg FREEBSD=${FREEBSD} \
		--build-arg GIT_VERSION=${VERSION} \
		--build-arg GIT_COMMIT=${COMMIT} \
		--build-arg BUILD_DATE=$$(date -u +'%Y-%m-%dT%H:%M:%SZ') \
		--dns 1.1.1.1 \
		--no-hosts \
		--volume ${CACHE}/pkg:/var/cache/pkg \
		--volume ${CACHE}/go-mod:/root/go/pkg/mod \
		--volume ${CACHE}/go-build:/root/.cache/go-build \
		-f Containerfile \
		-t ${IMAGE}:${VERSION} \
		-t ${IMAGE}:latest \
		.

oci-archive: oci # save the image as an OCI archive for deployment
	@rm -f ${IMAGE}.tar
	${PODMAN} save \
		--format oci-archive \
		--output ${IMAGE}.tar \
		${IMAGE}:${VERSION}
