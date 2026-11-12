.PHONY: all web build test vet fmt cross clean

all: web build

web:
	./scripts/build-web.sh

build:
	./scripts/build.sh

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -w .

cross:
	./scripts/build.sh darwin-arm64
	./scripts/build.sh darwin-amd64
	./scripts/build.sh linux-amd64
	./scripts/build.sh linux-arm64
	./scripts/build.sh windows-amd64

clean:
	rm -rf bin
