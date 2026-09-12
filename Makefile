BIN := fade-cli
PKG := ./cmd/fade-cli

# Default to a user-owned prefix so install needs no sudo. Override for a
# system-wide install: make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local

.PHONY: build debug test race fmt vet check install uninstall geocode clean dist

# -s -w strips symbol tables and DWARF (measured: 9.7 MB -> 6.7 MB), -trimpath
# drops absolute build paths. Use `make debug` for a symbolized binary.
build:
	go build -trimpath -ldflags '-s -w' -o $(BIN) $(PKG)

debug:
	go build -o $(BIN) $(PKG)

dist:
	sh scripts/release.sh

test:
	go test ./...

race:
	go test -race ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

# check is what CI should run. It builds too, so a broken target can't hide
# behind passing tests.
check: fmt vet build
	go test -race ./...

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)
	@echo "installed $(PREFIX)/bin/$(BIN) -- run '$(BIN)' to start"

uninstall:
	rm -f $(PREFIX)/bin/$(BIN)

# geocode fills coordinates for newly added shops, then rebuilds so the new
# data is embedded in the binary.
geocode:
	go run $(PKG) dev geocode
	$(MAKE) build

clean:
	rm -f $(BIN)
