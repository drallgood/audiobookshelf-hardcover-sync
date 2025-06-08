# Makefile for audiobookshelf-hardcover-sync

BINARY=main
VERSION ?= dev

.PHONY: build run test lint clean

build:
	go build -ldflags="-X main.version=$(VERSION)" -o $(BINARY) .

run: build
	./$(BINARY)

lint:
	go vet ./...
	golint ./...

test:
	go test -v ./...

clean:
	rm -f $(BINARY) audiobookshelf-hardcover-sync audiobookshelf-hardcover-sync.test sync-test test_build
	rm -f test_*.json debug_*.json
	go clean -testcache

docker-build:
	docker buildx build --load --build-arg VERSION=$(VERSION) -t ghcr.io/drallgood/audiobookshelf-hardcover-sync:$(VERSION) .

docker-run:
	docker run --rm -it -p 8080:8080 --env-file .env.example ghcr.io/drallgood/audiobookshelf-hardcover-sync:$(VERSION)
