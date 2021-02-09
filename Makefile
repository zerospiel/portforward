BUILDFLAGS:=CGO_ENABLED=0

.PHONY: test
test:
	go test -v $(CURDIR)/...

.PHONY: build
build:
	$(BUILDFLAGS) go build -o $(CURDIR)/bin/portforward $(CURDIR)/cmd/

.PHONY: lint
lint:
	golangci-lint run ./...