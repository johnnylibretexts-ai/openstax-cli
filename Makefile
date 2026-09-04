.PHONY: build test lint install clean

BIN_EXT := $(if $(filter windows,$(shell go env GOOS)),.exe,)

build:
	go build -o bin/openstax-pp-cli$(BIN_EXT) ./cmd/openstax-pp-cli

test:
	go test ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found; install it from https://golangci-lint.run/welcome/install/ (CI also runs it on every push)"; \
		exit 1; \
	}
	golangci-lint run

install: build
	go install ./cmd/openstax-pp-cli
	@mkdir -p $(HOME)/.local/bin
	cp bin/openstax-pp-cli$(BIN_EXT) $(HOME)/.local/bin/openstax-pp-cli$(BIN_EXT)
	ln -sf openstax-pp-cli$(BIN_EXT) $(HOME)/.local/bin/openstax-cli$(BIN_EXT)

clean:
	rm -rf bin/ openstax-cli openstax-pp-cli
