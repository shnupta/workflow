BINARY   := workflow
CMD      := ./cmd/workflow
TAGS     := fts5
GOFLAGS  := -tags $(TAGS)

.PHONY: build test run clean

build:
	go build $(GOFLAGS) -o $(BINARY) $(CMD)

test:
	go test $(GOFLAGS) ./...

run: build
	./$(BINARY) serve

clean:
	rm -f $(BINARY)
