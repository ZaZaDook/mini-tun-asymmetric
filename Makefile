GOOS_LINUX  := GOOS=linux GOARCH=amd64
GOOS_WIN    := GOOS=windows GOARCH=amd64
OUT         := ./dist

.PHONY: all master slave agent server-tui cli clean tls

all: master slave agent server-tui cli

master:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -o $(OUT)/mini-tun-asymmetric-master ./master/

slave:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -o $(OUT)/mini-tun-asymmetric-slave ./slave/

# The Windows GUI is the Flutter app in client-flutter/ (built with
# `flutter build windows`). The Go side is the privileged sidecar the GUI drives
# over loopback; build it here. The Flutter GUI links against client-windows/vpncore.
agent:
	mkdir -p $(OUT)
	$(GOOS_WIN) go build -ldflags="-H=windowsgui -s -w" -o "$(OUT)/mini-tun-asymmetric-agent.exe" ./cmd/mini-tun-asymmetric-agent/

server-tui:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -o $(OUT)/mini-tun-asymmetric-tui ./server-tui/

cli:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -o $(OUT)/mini-tun-asymmetric-cli ./cmd/mini-tun-asymmetric-cli/

# Generate self-signed TLS certs for development (requires openssl)
tls:
	mkdir -p certs
	openssl req -x509 -newkey rsa:4096 -keyout certs/master.key -out certs/master.crt \
		-days 365 -nodes -subj "/CN=mini-tun-asymmetric-master"
	cp certs/master.crt certs/ca.crt
	@echo "Certs written to ./certs/"

clean:
	rm -rf $(OUT)
