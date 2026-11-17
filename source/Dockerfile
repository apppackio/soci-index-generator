# Modified by AppPack from the upstream original (see NOTICE.txt).
FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

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
