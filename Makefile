IMAGE := soci-index-generator-build
ZIP := dist/soci_index_generator_lambda.zip
REGIONS := $(shell cat "$(CURDIR)/REGIONS.txt")

# BUILD_FLAGS exists so CI can pass --no-cache. The Dockerfile compiles and zips
# at image build time (the container only moves the finished zip out), so
# without it a second `make build` reuses cached layers and returns the same
# bytes, which proves nothing about reproducibility.
BUILD_FLAGS ?=

image:
	docker build --platform linux/amd64 $(BUILD_FLAGS) -t $(IMAGE) source

build: image
	mkdir -p dist
	docker run --rm --platform linux/amd64 -v "$(CURDIR)/dist:/output" $(IMAGE)

# Runs in the same pinned builder image as `build` (toolchain, apk deps and
# CGO_LDFLAGS_ALLOW come from source/Dockerfile). Tests the image's own copy of
# source/, so nothing is mounted and no root-owned files land in the checkout.
# The container's exit status is make's exit status.
test: image
	docker run --rm --platform linux/amd64 $(IMAGE) go test -mod=vendor ./... -skip TestHandler

# Two audit anchors. The zip hash covers exactly what Lambda is given. The
# bootstrap hash covers the compiled binary inside it, and survives a zip that
# changes only because the builder image's unpinned apk packages (zip) moved.
#
# Verifying, from dist/:
#   sha256sum -c soci_index_generator_lambda.zip.sha256
#   unzip -o soci_index_generator_lambda.zip bootstrap && sha256sum -c bootstrap.sha256
# bootstrap.sha256 names the binary inside the zip, which is not a loose file
# in dist/, so it has to be extracted first or the second check fails.
checksums:
	test -f "$(ZIP)" || { echo "$(ZIP) missing: run 'make build' first" >&2; exit 1; }
	cd dist && sha256sum soci_index_generator_lambda.zip > soci_index_generator_lambda.zip.sha256
	cd dist && unzip -p soci_index_generator_lambda.zip bootstrap | sha256sum | sed 's/  -$$/  bootstrap/' > bootstrap.sha256

# Uploads an already-built zip under a version-pinned key. It deliberately does
# not rebuild, and refuses to upload anything that does not match the
# .sha256 produced by `make checksums` (the file attached to the release), so a
# stale dist/ cannot go out under a real tag key. Run `make build checksums`
# first (the release workflow does). The key must name a tag: a mutable key
# cannot be audited, and CloudFormation would not notice a change.
#
# VERSION is an allowlist checked from the environment ($$VERSION, never
# expanded by make into shell text), so a hostile tag name cannot inject.
export VERSION
publish:
	printf '%s' "$$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$$' || { echo "VERSION must be a release tag like v1.2.3 (got '$$VERSION')" >&2; exit 1; }
	test -f "$(ZIP)" -a -f "$(ZIP).sha256" || { echo "$(ZIP) or its .sha256 missing: run 'make build checksums' first" >&2; exit 1; }
	cd dist && sha256sum -c soci_index_generator_lambda.zip.sha256
	test -s "$(CURDIR)/REGIONS.txt" -a -n "$(strip $(REGIONS))" || { echo "REGIONS.txt is missing or empty: refusing to publish to zero regions" >&2; exit 1; }
	count=0; \
	for region in $(REGIONS); do \
	    aws s3 cp "$(ZIP)" \
	      "s3://apppack-lambdas-$$region/soci-index-generator/$$VERSION/soci_index_generator_lambda.zip" \
	      --only-show-errors || exit 1; \
	    count=$$((count + 1)); \
	done; \
	test "$$count" -eq "$(words $(REGIONS))" || { echo "uploaded to $$count of $(words $(REGIONS)) regions" >&2; exit 1; }; \
	echo "uploaded to $$count regions"

# A non-reproducible build breaks the audit chain silently, so it is a failed
# build. Needs a finished `make build`. Hashes that build, rebuilds from scratch
# with --no-cache (the Dockerfile compiles at image build time, so a cached
# rebuild would return the same zip and compare it to itself), and compares the
# zip and the bootstrap binary (the stronger signal; zip framing depends on
# unpinned apk packages). Leaves the second build's dist/ and checksums in place.
repro-check:
	$(MAKE) checksums
	first="$$(mktemp -d)" && \
	cp dist/soci_index_generator_lambda.zip.sha256 dist/bootstrap.sha256 "$$first/" && \
	$(MAKE) clean && \
	$(MAKE) build BUILD_FLAGS=--no-cache && \
	$(MAKE) checksums && \
	cat "$$first/soci_index_generator_lambda.zip.sha256" dist/soci_index_generator_lambda.zip.sha256 "$$first/bootstrap.sha256" dist/bootstrap.sha256 && \
	cmp "$$first/bootstrap.sha256" dist/bootstrap.sha256 && \
	cmp "$$first/soci_index_generator_lambda.zip.sha256" dist/soci_index_generator_lambda.zip.sha256

clean:
	rm -rf dist

.PHONY: image build test checksums repro-check publish clean
