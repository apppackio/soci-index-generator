# apppack-soci-index-generator

A fork of the AWS SOCI index generator Lambda. AppPack runs it inside
customers' AWS accounts to give each app's container images a SOCI v2 index,
so Fargate can start tasks before the whole image has been pulled. Background
and design: [docs/design.md](docs/design.md).

## Why this repo is public

The function runs in your account with write access to your container image
repositories. This repo is public so you can answer one question: **is what is
running in my account what this repo says it is?** The procedure, and an
honest account of what it does and does not prove, is in
[docs/verifying.md](docs/verifying.md). Please read the "does not prove" parts.

## Provenance

- Upstream: https://github.com/awslabs/cfn-ecr-aws-soci-index-builder, Apache 2.0
- Vendored commit: 461a3f9e17810b9f80d6b293b5101003ff66d7fb (`functions/source/soci-index-generator-lambda/`)
- Builder image: golang:1.26-alpine@sha256:30b1e546a9c7fa3b747c2bdba3a0c7bdf3a02ebb4c5b8405d2f948d7e8aac4ec
  (digest-pinned; the `apk add` packages in it are not, see [docs/verifying.md](docs/verifying.md))
- Files changed or added relative to upstream: see [NOTICE.txt](NOTICE.txt).
  `source/vendor/` is byte-identical to upstream's.

## How this differs from upstream

1. **Moves the original tags onto the converted index** (upstream publishes
   `{tag}-soci`). AppPack deploys `{repo}:{commit}`, so a new tag would never be used.
2. **Loop prevention inspects the manifest, not the tag name.** Moving tags
   raises new push events; the generator skips any digest that is already an
   image index carrying a SOCI index, and skips (rather than converts) when it
   cannot tell.
3. **A size guard**: images over 6 GB compressed are skipped, not attempted.
   Lambda's 10 GB `/tmp` and 15 minute limit would otherwise wedge the function.
   It fails open: if ECR reports no size for an image, the guard cannot run,
   conversion proceeds, and a warning is logged and the metric carries
   `size_guard=not-enforced`.
4. **EMF metrics** (namespace `AppPack/SOCI`) for each invocation, because
   nothing breaks when conversion fails and nothing else would say so.

Upstream's separate **filter Lambda is intentionally absent**. The checks that
matter here (already converted, too large, event validation) run inside the
generator; scoping to one repository is done by the per-app EventBridge rule
in AppPack's stack, which is not in this repo.

## Build, test, release

Everything runs in the pinned builder container; the host needs only Docker.

```bash
make test        # unit tests, in the builder image
make build       # dist/soci_index_generator_lambda.zip
make checksums   # dist/*.sha256 for the zip and the bootstrap binary
```

- Builds and tests use `--platform linux/amd64`, matching the Lambda. On an
  arm64 host (Apple silicon) Docker emulates it, so a local build takes
  minutes.
- `CGO_LDFLAGS_ALLOW='-l:libz\.a'` in `source/Dockerfile` is deliberate. Go
  1.26.11 (`golang:1.26-alpine`) rejects the vendored soci-snapshotter's `-l:libz.a` cgo flag with
  `invalid flag in #cgo LDFLAGS` (reproduced against an untouched upstream copy). The
  allowlist re-permits exactly that one flag and nothing broader; it is not a
  general relaxation of cgo's flag checking.
- CI (`.github/workflows/ci.yml`) tests, builds, then rebuilds with
  `--no-cache` and fails if the zip or binary hash differs.
- **Releasing:** push a tag `vX.Y.Z`. The release workflow builds, creates the
  GitHub release (zip plus both checksum files), then uploads the zip to
  `s3://apppack-lambdas-<region>/soci-index-generator/<tag>/` for each region in
  `REGIONS.txt`. **Published tags are never deleted or re-pointed**; see
  [docs/verifying.md](docs/verifying.md#releases-are-immutable-by-policy).

## Repository setup

Renovate runs self-hosted (`.github/workflows/renovate*.yml`, configured by
`renovate.json`) and automerges. That is safe only because merging is not
releasing and CI gates the merge. The CI gate exists only if these hold, and
none of them is enforced by this repo's files:

- **Branch protection on `main` requires the CI status check.** Without it,
  automerge merges unchecked changes.
- **The required check name matches the job's name.** The job in `ci.yml` is
  `ci`; a required check named anything else is never reported, and protection
  then passes vacuously or blocks forever.
- **The repo setting "Allow auto-merge" is enabled.** Otherwise
  `platformAutomerge` does nothing.
- **After the first Renovate run**, confirm `go mod vendor` under the `1.26`
  constraint produces **no diff** against the committed `source/vendor/`. A
  diff is exactly the toolchain skew the constraint exists to prevent.

## Upstream sync

Renovate keeps Go modules, the builder image and GitHub Actions current. It does
not track the upstream fork source (`awslabs/cfn-ecr-aws-soci-index-builder`),
so syncing with it is a manual, occasional job. A green Dependency Dashboard
does not mean this repo is current with upstream.
