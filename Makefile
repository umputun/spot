B=$(shell git rev-parse --abbrev-ref HEAD)
BRANCH=$(subst /,-,$(B))
GITREV=$(shell git describe --abbrev=7 --always --tags)
REV=$(GITREV)-$(BRANCH)-$(shell date +%Y%m%d-%H:%M:%S)


all: test build

build:
	cd app && go build -ldflags "-X main.revision=$(REV) -s -w" -o ../.bin/spot.$(BRANCH)

release:
	- @mkdir -p bin
	docker build -f Dockerfile.release --progress=plain -t spot.bin --no-cache .
	- @docker rm -f spot.bin 2>/dev/null || exit 0
	docker run -d --name=spot.bin spot.bin
	docker cp spot.bin:/artifacts .bin/
	docker rm -f spot.bin

test:
	cd app && go clean -testcache
	cd app && go test -race -coverprofile=../coverage.out ./...
	grep -v "_mock.go" coverage.out | grep -v mocks > coverage_no_mocks.out
	go tool cover -func=coverage_no_mocks.out
	rm coverage.out coverage_no_mocks.out

site:
	@rm -f  site/public/*
	@docker rm -f spot-site
	docker build -f Dockerfile.site --progress=plain -t spot.site .
	docker run -d --name=spot-site spot.site
	sleep 3
	docker cp "spot-site":/srv/site/ site/public
	docker rm -f spot-site
#	rsync -avz -e "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" --progress ./site/public/ reproxy.io:/srv/www/reproxy.io

.PHONY: build release test site