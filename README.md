# apppack-soci-index-generator

Fork of the AWS SOCI index generator Lambda, used by AppPack to build SOCI v2 indexes.
See docs/design.md.

## Provenance

- Upstream: https://github.com/awslabs/cfn-ecr-aws-soci-index-builder
- Vendored commit: 461a3f9e17810b9f80d6b293b5101003ff66d7fb (`functions/source/soci-index-generator-lambda/`)
- Builder image: golang:1.26-alpine@sha256:30b1e546a9c7fa3b747c2bdba3a0c7bdf3a02ebb4c5b8405d2f948d7e8aac4ec

## Upstream sync

Renovate keeps Go modules, the builder image and GitHub Actions current. It does
not track the upstream fork source (`awslabs/cfn-ecr-aws-soci-index-builder`),
so syncing with it is a manual, occasional job. A green Dependency Dashboard
does not mean this repo is current with upstream.
