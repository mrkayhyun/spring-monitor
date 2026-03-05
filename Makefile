BINARY   := sm
MODULE   := github.com/mrkayhyun/spring-monitor
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION) -s -w"

.PHONY: build build-linux install clean run tidy

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/sm

build-linux:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-amd64 ./cmd/sm

build-linux-arm64:
	@mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-arm64 ./cmd/sm

install: build
	install -m 755 bin/$(BINARY) /usr/local/bin/$(BINARY)

uninstall:
	rm -f /usr/local/bin/$(BINARY)

run:
	go run ./cmd/sm

tidy:
	go mod tidy

clean:
	rm -rf bin/
