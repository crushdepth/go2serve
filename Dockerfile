FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o go2serve .
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
