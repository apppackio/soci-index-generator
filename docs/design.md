# SOCI index Lambda

Status: design, not yet built. Written 2026-11-11.

## Intent

Make ECS Fargate tasks start faster by giving every AppPack app image a SOCI
v2 index, so Fargate's SOCI snapshotter can start a container before the whole
image has been pulled.

This is a pure optimization layer. It sits beside the build and deploy path
and never participates in it. If every part of this system stops working, the
only consequence is that tasks start as slowly as they do today.

## Why now

Measured on a real app (`ll-staging-web`, 627 MiB image, Fargate platform
1.4.0, 1024 cpu / 2048 mem), 5 tasks per arm, interleaved:

| Metric (median)             | Baseline | SOCI v2 | Delta         |
|-----------------------------|----------|---------|---------------|
| Image pull                  | 29.6s    | 4.3s    | −25.3s (−85%) |
| Task → RUNNING              | 42.1s    | 16.3s   | −25.8s (−61%) |
| Task → application serving  | 47.1s    | 32.1s   | −15.0s (−32%) |

Ranges did not overlap: baseline 39.8–49.4s to serving, SOCI 27.8–38.1s. Note
the third row, not the first — roughly 10s of the pull saving is repaid later
as lazy reads while the application starts. 15s is the honest number.

Cost: the index adds about 5% to stored image size (+26.7 MiB on 532.6 MiB of
artifacts). 10 of 18 layers got zTOCs; the other 8 fell under the 10 MiB
threshold and were skipped.

## Desired final state

For every AppPack app, in every account and region, with no customer action
and no change to any task definition:

1. A build finishes and pushes `{ecr-repo}:{commit}` as it does today.
2. Within a minute or two, that tag no longer points at a plain image
   manifest. It points at an OCI image index containing two mutually
   annotated manifests: the application image, and a SOCI v2 index
   (`application/vnd.amazon.soci.index.v2+json`).
3. Fargate detects the index on its own and uses lazy loading. Nothing in the
   task definition says "SOCI"; nothing needs to.
4. Any task that starts *after* that point — autoscaling, task replacement,
   a rollback to that tag, a restart — gets the faster start.

The deploy that triggered the build is explicitly **not** covered. It will
almost always win the race against conversion, so it starts at today's speed.
That is a deliberate first iteration (see Non-goals).

Verifying the final state from outside: the tag resolves to
`application/vnd.oci.image.index.v1+json`, and one of its manifests carries
`artifactType: application/vnd.amazon.soci.index.v2+json`.

## Architecture

Reuse [awslabs/cfn-ecr-aws-soci-index-builder](https://github.com/awslabs/cfn-ecr-aws-soci-index-builder)
(Apache 2.0). Its architecture is already the one we want:

```
ECR push ──> EventBridge rule ──> filter Lambda ──> generator Lambda ──> ECR
 (build)      ECR Image Action    (Python; is     (Go; soci.Convert,   (push index,
              PUSH / SUCCESS       this ours?      work in /tmp)        move the tag)
                                   already done?)
```

Four properties matter more than any of the components:

**It is triggered by ECR, not by the builder.** The build does not call it,
wait for it, or know it exists. This is what keeps a conversion failure from
ever becoming a deploy failure, and it is why the feature ships without
touching `codebuild-image`.

**It must not trigger itself.** The generator's own push into the same
repository raises another PUSH event. See "The one real change" below — this
is where we diverge from upstream, and it is the single most important
correctness property in the design.

**Conversion needs no daemon.** The generator calls `soci.Convert()` from the
soci-snapshotter library against a local OCI store under `/tmp`. No containerd
service, no Docker, no privileged runtime.

**Conversion does not duplicate layers.** The converted manifest references
the same blobs. What gets added is the zTOC artifact and two small manifests.
The image manifest gains an annotation, so its digest changes — which is fine
here only because AppPack is tag-based end to end, from `containers.go`
(`Image string // {repo}:{commit}`) through `postbuild.go`. Nothing in the
system pins a digest.

### Where it lives

In the per-app stack, with its own EventBridge rule scoped to that app's ECR
repository. Every app carries its own copy, and the duplication is fine:
Lambda bills on invocation and the function is idle between builds. An app's
indexing is then provisioned, versioned, and torn down with the app itself,
scoped to exactly one repository, and needs no account-wide permission grant.

## The one real change from upstream

**Upstream publishes the converted index under a new tag; we must repoint the
original tag instead.**

For V2, upstream tags the converted image index `{original-tag}-soci`
(`handler.go`, `tag = originalTag + "-soci"`), and the filter Lambda breaks the
trigger loop by skipping any pushed tag ending in `-soci`.

That is fatal for AppPack as-is. ECS deploys `{repo}:{commit}`, which upstream
leaves pointing at the original unindexed manifest, so no task would ever get
lazy loading — not on deploy, not on scale-out, not ever. The benefit only
reaches a workload that knows to ask for the `-soci` tag, and ours cannot,
because under the async model that tag may not exist yet when the deploy runs.

So the generator must push the converted index to the **original** tag, moving
it. That is what makes the whole thing transparent: one tag, task definitions
untouched, Fargate auto-detecting. Verified in the benchmark — a tag resolving
to an image index with a SOCI v2 manifest is picked up with no task definition
change. (The benchmark pushed to a fresh tag rather than moving an existing
one; the resulting structure is identical, but the move itself is untested.)

Changing the tag forces a second change: **loop prevention can no longer key
off the tag name.** The re-push looks like an ordinary push of `{commit}`. It
has to be detected by inspecting what the digest actually is — if it is already
an OCI image index carrying a SOCI index, stop. The primitive already exists
upstream as `registry.validateImageIndex`, used there for validation rather
than as a skip condition.

Get this wrong and every build spawns an unbounded loop of Lambda invocations
against ECR. It deserves a test of its own.

## Reuse plan

**Vendor unchanged:**

- `functions/source/soci-index-generator-lambda/` — the Go generator, minus
  the tagging change above. This is where the hard parts live: the
  `soci.Convert()` call, the ECR auth dance, the OCI store handling, and the
  `/tmp` free-space accounting. Keep the vendored dependency tree; it is
  already checked in upstream.
- Its `Dockerfile` and `Makefile`, which build the function to a zip
  (`soci_index_generator_lambda.zip`). Note this is a **zip-packaged Lambda
  built inside a container**, not a container-image Lambda; there is no
  official prebuilt image to reference, and nothing is published to ECR Public
  or as a release artifact.
- The event validation in the Python filter, which is thorough and worth
  keeping as-is.

**Adapt:**

- The tagging and loop-prevention behavior described above.
- The filter's scoping. Upstream matches `repository-name:image-tag` against
  a comma-separated list of fnmatch patterns in the
  `soci_repository_image_tag_filters` env var. For a per-app stack this
  becomes that app's single repository, which is a configuration change rather
  than a code change.

**Discard:**

- `templates/SociIndexBuilder.yml`. Upstream's CloudFormation is an
  account-level deployment with its own parameters and taskcat scaffolding.
  The resources it creates are the reference; the template is not reusable in
  a per-app stack.

**Carry forward:** Apache 2.0 requires preserving the license and NOTICE for
vendored code. Upstream's module path still says `aws-ia/...` from before the
repo moved to `awslabs`.

**Verify early:** upstream's `go.mod` pins `soci-snapshotter v0.11.1`, which
is older than the v0.13.0 that shipped the `convert` CLI subcommand, though
`soci_convert.go` is present in its vendored tree. Confirm the pinned version
emits a v2 index that current Fargate accepts before building anything around
it — new Fargate customers cannot use v1, so this is load-bearing.

## Scale ceiling

Lambda caps ephemeral storage at 10 GB and runtime at 15 minutes.

The generator does its work in `/tmp` and logs a warning when free space is
under 6 GB, with the comment "this is problematic because we support images as
big as 6GB". Upstream documents **a maximum compressed image size of 6 GB**,
and describes the failure mode as the generator timing out at 15 minutes.

**Guard below 6 GB compressed**, and enforce it in the filter, where it is
free: ECR reports compressed layer sizes in the manifest, so an oversized
image can be skipped before pulling a byte. An image over the ceiling must
log, emit a signal, and leave the image alone — never retry forever, never
half-convert.

For scale, the benchmark image is 627 MiB and typical application images run
200 MB to 1.5 GB. The guard excludes nothing real; it exists to stop a
pathological image from wedging the function.

zTOC generation streams through compressed layers rather than decompressing
them to disk, so uncompressed size never enters into it, and memory is not the
constraint. Time has room to spare: conversion measured 8s at 627 MiB, and an
in-region ECR pull is far faster than the 100s seen pulling over the internet
during the benchmark.

## Things that will bite

**V2 skips untagged images.** Upstream returns early with "Skipping SOCI index
generation for V2 as image has no tag". Any AppPack flow that pushes by digest
alone gets no index, silently.

**Which tags get repointed.** `build.go:223` uses `crane.Tag` to add
`build-NNN` and `latest` aliases at build time, pointing at the original
manifest. If the generator moves only `{commit}`, those aliases still resolve
to the unconverted image, and a rollback through `build-NNN` quietly gets no
SOCI. Decide deliberately whether to move every tag resolving to the source
digest, or only the commit tag.

**The ECR lifecycle policy, which is the sharpest edge here.**
`formations/app/build.py:130` gives every app repository a single rule:

```python
"selection": {
    "tagStatus": "any",
    "countType": "imageCountMoreThan",
    "countNumber": 50,          # 200 for pipeline repositories
},
"action": {"type": "expire"},
```

Today a build produces one manifest. After conversion it produces up to four:
the original image manifest (now untagged), the converted image manifest, the
SOCI index manifest, and the image index that carries the tag. Against a rule
that counts images with `tagStatus: any`, that is a multiplier.

Left alone, "keep the 50 most recent images" silently becomes roughly "keep
the last 12 builds", and rollback range collapses with nothing announcing it.

**Recommended replacement — count builds, not manifests:**

```json
{"rules": [
  {"rulePriority": 1,
   "description": "Keep 50 most recent builds",
   "selection": {"tagStatus": "tagged", "tagPrefixList": ["build-"],
                 "countType": "imageCountMoreThan", "countNumber": 50},
   "action": {"type": "expire"}},
  {"rulePriority": 2,
   "description": "Reap manifests orphaned by conversion and by rule 1",
   "selection": {"tagStatus": "untagged",
                 "countType": "sinceImagePushed", "countUnit": "days",
                 "countNumber": 14},
   "action": {"type": "expire"}}
]}
```

`tagStatus: any` counts manifests, which makes retention depend on an
implementation detail of SOCI. Every build gets exactly one `build-NNN` tag
from `crane.Tag`, so counting that prefix counts builds and is immune to how
many manifests conversion adds. `countNumber` stays at 50 and 200 — the intent
is unchanged, the policy is just made to mean what it already claimed.

**This settles the "which tags move" question.** For rule 1 to count the right
thing, the generator must move `build-NNN` and `latest` along with `{commit}`.
Moving only `{commit}` leaves `build-NNN` on the original manifest, so rule 1
counts originals while the converted indexes — tagged, so untouched by rule 2;
wrong prefix, so uncounted by rule 1 — accumulate forever. Moving all three
also means a rollback through `build-NNN` gets SOCI.

**Rule 2 is load-bearing, and is the thing to verify before shipping.** It
cannot be dropped: orphaned manifests are small, but ECR only collects layers
once no manifest references them, so leaving orphans pins layers forever and
storage grows without bound. It is also the rule that would delete SOCI
children if ECR does not in fact protect manifests referenced by a live index.
Reports say it does, and that attestation manifests behave the same way, but
this is not confirmed in AWS documentation — and a 14-day window means
discovering otherwise two weeks after shipping, on images people are still
rolling back to.

Verify directly: push a converted index to a scratch repository carrying a
1-day untagged rule and check whether the children survive. Cheap, definitive,
and a prerequisite.

Tag mutability is fine: `build.py:148` does not set `ImageTagMutability`, so
repositories default to `MUTABLE` and moving a tag is permitted. Had it been
`IMMUTABLE`, the whole tag-moving approach would be impossible and upstream's
`-soci` suffix would be the only option.

**Idempotency.** EventBridge delivers at least once and Lambda retries.
Converting the same digest twice must converge rather than compound. Treating
the source digest as the unit of work makes this mostly free, and soci skips
layers whose zTOCs already exist unless forced.

**Observability.** Because nothing breaks when this fails, nothing will tell
you it has failed. It needs its own signal — at minimum, whether the tag ends
up pointing at an index, and how long conversion took.

## Non-goals

- **Making the triggering deploy faster.** Accepted explicitly. The first
  iteration buys faster scale-out and task recycling only.
- **Changing the build.** No change to `codebuild-image`, no added build time,
  no new build dependency.
- **Changing task definitions.** Fargate auto-detects.
- **Customer opt-in.** It runs for every app.
- **SOCI v1.** New Fargate customers cannot use it.

## Anti-goals

Any of these means the design failed even if indexes are being produced:

- A conversion failure delays, blocks, or breaks a deploy.
- The Lambda triggers itself, in any circumstance.
- An image is deleted, corrupted, or left in a state ECS cannot pull.
- A customer has to know this exists.

## Options not taken

**Conversion inline in the build.** Measured at about 19s of added build time:
+7s for a second OCI export, +11s for `soci convert` (CPU-bound zTOC work),
+1s to copy artifacts. This makes the triggering deploy benefit immediately,
because the index exists before ECS is told to deploy. Set aside because it
taxes every build to help some deploys, and because the async path ships
without touching the builder.

**Having the deploy wait for the index.** Inverts the race: the build pushes
an interim tag, the Lambda converts and publishes `{commit}`, and the deploy
waits for it to appear. Every deploy would then get the 15s. Set aside because
it couples the deploy path to this system, which is exactly what the anti-goals
forbid in iteration one.

Both become worth revisiting once the async version has shown the indexes are
reliable and the scale-out win is real.
