# Makefile for audiobookshelf-hardcover-sync

BINARY=main
VERSION ?= dev

.PHONY: build run test lint

build:
	go build -ldflags="-X main.version=$(VERSION)" -o $(BINARY) .

run: build
	./$(BINARY)

lint:
	go vet ./...
	golint ./...

test:
	go test -v ./...

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/drallgood/audiobookshelf-hardcover-sync:$(VERSION) .

docker-run:
	docker run --rm -it -p 8080:8080 --env-file .env.example ghcr.io/drallgood/audiobookshelf-hardcover-sync:$(VERSION)
