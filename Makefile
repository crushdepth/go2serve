PROJECT := go2serve
BIN     := go2serve

# Reproducible build flags, identical to the Dockerfile build line. -trimpath
# strips local paths; -buildvcs=false drops the VCS stamp so builds are
# deterministic across git/tarball checkouts.
GO_BUILD_FLAGS := -trimpath -buildvcs=false -ldflags="-s -w"

.PHONY: build up down restart logs status build-bin verify tag

build:
	docker compose -p $(PROJECT) build

up: build
	docker compose -p $(PROJECT) up -d

down:
	docker compose -p $(PROJECT) down

restart:
	docker compose -p $(PROJECT) restart

logs:
	docker compose -p $(PROJECT) logs -f

status:
	docker compose -p $(PROJECT) ps

# Native reproducible build (no Docker). Requires Go; the toolchain pinned in
# go.mod ensures the same compiler version is used even if a newer Go is installed.
build-bin:
	CGO_ENABLED=0 go build $(GO_BUILD_FLAGS) -o $(BIN) .

# Verify dependencies against go.sum, then build and print the binary's sha256 so two
# independent builders (on the same OS/arch) can confirm an identical artifact.
verify:
	go mod verify
	$(MAKE) build-bin
	@sha256sum $(BIN)

# Cut a signed release tag:  make tag TAG=vX.Y.Z
# Requires a git signing key (see SECURITY.md). Verify with: git verify-tag <tag>
tag:
	@test -n "$(TAG)" || { echo "usage: make tag TAG=vX.Y.Z"; exit 1; }
	git tag -s $(TAG) -m "$(TAG)"
	@echo "Signed tag $(TAG) created. Verify with: git verify-tag $(TAG)"
