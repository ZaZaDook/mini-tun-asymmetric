GOOS_LINUX  := GOOS=linux GOARCH=amd64
GOOS_WIN    := GOOS=windows GOARCH=amd64
OUT         := ./dist

.PHONY: all master slave client-windows server-tui cli clean tls

all: master slave client-windows server-tui cli

master:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -o $(OUT)/mini-tun-asymmetric-master ./master/

slave:
	mkdir -p $(OUT)
	$(GOOS_LINUX) go build -o $(OUT)/mini-tun-asymmetric-slave ./slave/

client-windows:
	mkdir -p $(OUT)
	$(GOOS_WIN) go build -ldflags="-H windowsgui" -o "$(OUT)/Mini-Tun Asymmetric.exe" ./client-windows/

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
