# Base image pinned by immutable digest (multi-arch index, so arm64 is preserved).
# The tag (1.26.3-alpine) is informational; the @sha256 digest is what actually
# fixes the image. Keep the Go patch in lockstep with the toolchain in go.mod.
# To bump: docker buildx imagetools inspect golang:1.26-alpine, then update both.
FROM golang:1.26.3-alpine@sha256:91eda9776261207ea25fd06b5b7fed8d397dd2c0a283e77f2ab6e91bfa71079d AS builder
WORKDIR /build
COPY go.mod go.sum ./
# go mod verify fails the build if any cached module deviates from go.sum.
RUN go mod download && go mod verify
COPY . .
# Reproducible build flags: -trimpath strips local filesystem paths; -buildvcs=false
# drops the embedded VCS revision/time/dirty stamp so a git-tree build and a tarball
# build of the same commit produce byte-identical binaries.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o go2serve .
RUN echo "go2serve:x:10847:10847::/:" > /etc/go2serve-passwd \
 && echo "go2serve:x:10847:" > /etc/go2serve-group \
 && mkdir /certs && chown 10847:10847 /certs

FROM scratch
COPY --from=builder /build/go2serve /go2serve
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/go2serve-passwd /etc/passwd
COPY --from=builder /etc/go2serve-group /etc/group
COPY --from=builder --chown=10847:10847 /certs /certs
USER go2serve:go2serve
EXPOSE 8080 8443
ENTRYPOINT ["/go2serve"]
