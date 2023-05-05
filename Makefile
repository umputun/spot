# get the latest commit branch, hash and date
BRANCH=$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)
HASH=$(shell git rev-parse --short=7 HEAD 2>/dev/null)
TIMESTAMP=$(shell git log -1 --format=%ct HEAD 2>/dev/null | xargs -I{} date -u -r {} +%Y%m%dT%H%M%S)
GIT_REV=$(shell printf "%s-%s-%s" "$(BRANCH)" "$(HASH)" "$(TIMESTAMP)")
REV=$(if $(filter --,$(GIT_REV)),latest,$(GIT_REV)) # fallback to latest if not in git repo

all: test build

build:
	cd cmd/spot && go build -ldflags "-X main.revision=$(REV) -s -w" -o ../../.bin/spot.$(BRANCH)

release:
	- @mkdir -p bin
	docker build -f Dockerfile.release --progress=plain -t spot.bin --no-cache .
	- @docker rm -f spot.bin 2>/dev/null || exit 0
	docker run -d --name=spot.bin spot.bin
	docker cp spot.bin:/artifacts .bin/
	docker rm -f spot.bin

test:
	go clean -testcache
	go test -race -coverprofile=coverage.out ./...
	grep -v "_mock.go" coverage.out | grep -v mocks > coverage_no_mocks.out
	go tool cover -func=coverage_no_mocks.out
	rm coverage.out coverage_no_mocks.out

version:
	@echo "branch: $(BRANCH), hash: $(HASH), timestamp: $(TIMESTAMP)"
	@echo "revision: $(REV)"

site:
	@rm -f  site/public/*
	@docker rm -f spot-site
	docker build -f Dockerfile.site --progress=plain -t spot.site .
	docker run -d --name=spot-site spot.site
	sleep 3
	docker cp "spot-site":/srv/site/ site/public
	docker rm -f spot-site
#	rsync -avz -e "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" --progress ./site/public/ simplotask.com:/srv/www/simplotask.com

.PHONY: build release test site