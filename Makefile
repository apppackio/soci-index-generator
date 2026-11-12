IMAGE := soci-index-generator-build
ZIP := dist/soci_index_generator_lambda.zip
REGIONS := $(shell cat "$(CURDIR)/REGIONS.txt")

image:
	docker build --platform linux/amd64 -t $(IMAGE) source

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
# Both files are in `sha256sum -c` format, run from dist/.
checksums:
	test -f "$(ZIP)" || { echo "$(ZIP) missing: run 'make build' first" >&2; exit 1; }
	cd dist && sha256sum soci_index_generator_lambda.zip > soci_index_generator_lambda.zip.sha256
	cd dist && unzip -p soci_index_generator_lambda.zip bootstrap | sha256sum | sed 's/  -$$/  bootstrap/' > bootstrap.sha256

# Uploads an already-built zip under a version-pinned key. It deliberately does
# not rebuild, so what is uploaded is the file that was hashed and released.
# Run `make build` first (the release workflow does). The key must name a tag:
# a mutable key cannot be audited, and CloudFormation would not notice a change.
# Checked at parse time so a bad VERSION fails before anything else runs.
ifneq ($(filter publish,$(MAKECMDGOALS)),)
ifneq ($(words $(VERSION)),1)
$(error VERSION must be set to a release tag, e.g. make publish VERSION=v1.2.3)
endif
ifneq ($(filter v%,$(VERSION)),$(VERSION))
$(error VERSION must start with 'v' (got '$(VERSION)'))
endif
ifneq ($(patsubst v%,%,$(VERSION)),$(subst /,,$(patsubst v%,%,$(VERSION))))
$(error VERSION must not contain '/' (got '$(VERSION)'))
endif
ifeq ($(patsubst v%,%,$(VERSION)),)
$(error VERSION must be a tag like v1.2.3 (got '$(VERSION)'))
endif
endif

publish:
	test -f "$(ZIP)" || { echo "$(ZIP) missing: run 'make build' first" >&2; exit 1; }
	for region in $(REGIONS); do \
	    aws s3 cp "$(ZIP)" \
	      "s3://apppack-lambdas-$$region/soci-index-generator/$(VERSION)/soci_index_generator_lambda.zip" \
	      --only-show-errors || exit 1; \
	done

clean:
	rm -rf dist

.PHONY: image build test checksums publish clean
