FROM golang:1.26-alpine@sha256:30b1e546a9c7fa3b747c2bdba3a0c7bdf3a02ebb4c5b8405d2f948d7e8aac4ec AS builder

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
