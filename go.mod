module go2serve

go 1.26

// Pin the toolchain (not just a minimum) so the compiler version is fixed.
// Reproducible builds hold only for a fixed Go version; this anchors both the
// native build (make build-bin) and the Docker build to the same compiler.
// Keep this in lockstep with the base image tag in the Dockerfile.
toolchain go1.26.3

require (
	golang.org/x/crypto v0.51.0
	golang.org/x/net v0.55.0
)

require golang.org/x/text v0.37.0 // indirect
