build:
	docker build --platform linux/amd64 -t soci-index-generator-build source
	mkdir -p dist
	docker run --rm --platform linux/amd64 -v $(PWD)/dist:/output soci-index-generator-build

test:
	cd source && go test -mod=vendor ./... -skip TestHandler

clean:
	rm -rf dist

.PHONY: build test clean
