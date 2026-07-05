GOOS_LINUX  := GOOS=linux GOARCH=amd64
GOOS_WIN    := GOOS=windows GOARCH=amd64
OUT         := ./dist
VERSION     := $(shell tr -d '[:space:]' < VERSION)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.PHONY: all master slave agent server-tui mta-setup cli clean tls \
        packages deb rpm client-portable client-installer all-packages

all: master slave agent server-tui cli

master:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -ldflags="$(LDFLAGS)" -o $(OUT)/mini-tun-asymmetric-master ./master/

slave:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -ldflags="$(LDFLAGS)" -o $(OUT)/mini-tun-asymmetric-slave ./slave/

# The Windows GUI is the Flutter app in client-flutter/ (built with
# `flutter build windows`). The Go side is the privileged sidecar the GUI drives
# over loopback; build it here. The Flutter GUI links against client-windows/vpncore.
agent:
	mkdir -p $(OUT)
	$(GOOS_WIN) go build -ldflags="-H=windowsgui $(LDFLAGS)" -o "$(OUT)/mini-tun-asymmetric-agent.exe" ./cmd/mini-tun-asymmetric-agent/

# server-tui is the mta-setup wizard/manager (same binary).
server-tui mta-setup:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -ldflags="$(LDFLAGS)" -o $(OUT)/mta-setup ./server-tui/

cli:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -ldflags="$(LDFLAGS)" -o $(OUT)/mini-tun-asymmetric-cli ./cmd/mini-tun-asymmetric-cli/

# ── Packaging ────────────────────────────────────────────────────────────────
# Linux server packages (.deb + .rpm). Requires nfpm:
#   go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
packages deb rpm:
	bash packaging/build.sh

# Windows client portable zip (Flutter GUI + agent + wintun). Requires flutter.
client-portable:
	bash packaging/build-windows.sh

# Windows installer (.exe). Requires Inno Setup 6 + a prior client-portable build.
client-installer:
	bash packaging/build-installer.sh

# Everything that can be built on this host.
all-packages: packages client-portable

# Generate self-signed TLS certs for development (requires openssl)
tls:
	mkdir -p certs
	openssl req -x509 -newkey rsa:4096 -keyout certs/master.key -out certs/master.crt \
		-days 365 -nodes -subj "/CN=mini-tun-asymmetric-master"
	cp certs/master.crt certs/ca.crt
	@echo "Certs written to ./certs/"

clean:
	rm -rf $(OUT)
