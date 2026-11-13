# Modified by AppPack from the upstream original (see NOTICE.txt).
FROM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS builder

# Install only essential build dependencies
RUN apk add --no-cache \
    make \
    git \
    zip \
    gcc \
    musl-dev \
    zlib-dev \
    zlib-static

WORKDIR /build

COPY . .

# Newer Go patch releases reject the vendored soci-snapshotter's `-l:libz.a` cgo flag.
# Allow exactly that flag (and nothing broader).
ENV CGO_LDFLAGS_ALLOW='-l:libz\.a'

RUN make -f Makefile

CMD mkdir -p /output/ && mv /build/soci_index_generator_lambda.zip /output/
