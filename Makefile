IMAGE := soci-index-generator-build

image:
	docker build --platform linux/amd64 -t $(IMAGE) source

build: image
	mkdir -p dist
	docker run --rm --platform linux/amd64 -v $(PWD)/dist:/output $(IMAGE)

# Runs in the same pinned builder image as `build` (toolchain, apk deps and
# CGO_LDFLAGS_ALLOW come from source/Dockerfile). Tests the image's own copy of
# source/, so nothing is mounted and no root-owned files land in the checkout.
# The container's exit status is make's exit status.
test: image
	docker run --rm --platform linux/amd64 $(IMAGE) go test -mod=vendor ./... -skip TestHandler

clean:
	rm -rf dist

.PHONY: image build test clean
