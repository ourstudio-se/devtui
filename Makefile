BINARY := devtui

.PHONY: build run install clean vet

build:
	go build -o $(BINARY) ./cmd/devtui

run: build
	./$(BINARY)

install:
	go install ./cmd/devtui

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
