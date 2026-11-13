# Live verification results

## STATUS: RUN — all five answered

Run on 2026-11-13 against the canary app's stack (`ll-staging`), using the
released v0.1.1 artifact and its per-app Lambda. This is the production path,
not a scratch function.

| Field | Value |
|---|---|
| Date run | 2026-11-13 |
| Region | us-east-1 |
| Account | 135549385118 |
| Image / repository | `ll-staging` / `apppack-app-ll-staging-ecrrepository-6ml4y68rejsn` |
| Release zip version | v0.1.1 |
| Run by | Canary builds #556 (SOCI) and #557 (plain, no-ARN test) |

## Setup (needed for questions 1-5)

1. Deploy the released zip as a Lambda in a scratch account: `provided.al2023`,
   10 GB ephemeral storage, `soci_index_version=V2`, `APP_NAME=verify`, with
   the ECR permissions the companion plan grants.
2. Push a real app image (ideally ~600 MiB, to compare with the spec's
   benchmark) to an ECR repository, tagged `{commit}`, `build-NNN`, `latest`.
3. Invoke the function with a synthetic `ECR Image Action` event for that push.

## Q1. Does `soci.Convert()` with pinned soci-snapshotter v0.16.1 emit a v2 index at all?

Run: `./verify/index_structure.sh <repo> <commit-tag>`; read the printed
`imageManifestMediaType` and manifest.

- Pass: media type is `application/vnd.oci.image.index.v1+json` and the index
  has a child that is a SOCI index (script exits 0).
- Fail obliges: stop. The prerequisite in the spec ("new Fargate customers
  cannot use v1") is unmet. Revisit the pin, or the conversion options, before
  building anything further.

Result: PASS. `imageManifestMediaType: application/vnd.oci.image.index.v1+json`;
`index_structure.sh` reported `PASS: OCI index with 1 SOCI v2 child
descriptor(s)` for `build-556` and for the restored `build-557`.

## Q2. Does the SOCI child descriptor carry `artifactType` where the guard looks? (BLOCKING)

`IsSociConvertedIndex` (`source/utils/registry/soci.go`) returns true only if a
descriptor in the parent index's `manifests[]` has
`artifactType: application/vnd.amazon.soci.index.v2+json`. Every unit test
builds that structure by hand; none shows a real conversion produces it.

Run: the same `index_structure.sh` command as Q1. It checks exactly this field
(`.manifests[].artifactType`), and prints the raw JSON.

- Pass: script exits 0, and the pasted JSON visibly shows `artifactType` on a
  child descriptor.
- Fail obliges: **the loop guard never fires and the Lambda can trigger
  itself, the spec's absolute anti-goal. Do not ship.** Stop and revise Task 2
  (`IsSociConvertedIndex`) using the real structure in the JSON below. Also
  re-run Q5 after the fix.

Raw index manifest JSON from a real conversion:

```
imageManifestMediaType: application/vnd.oci.image.index.v1+json

{
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": null,
  "digest": "sha256:569a085b48676441d8d6906a4fd1063691e892c11f5dfb8ed1485af48a0a1d71"
}
{
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": "application/vnd.amazon.soci.index.v2+json",
  "digest": "sha256:5a64e872479d7667fb108c633df7cbbb3b64b164c07a0a155ad1fb7f31308fa9"
}
```

Result: PASS. The parent media type is the OCI image index and the second child
descriptor carries `artifactType: application/vnd.amazon.soci.index.v2+json`,
exactly where `IsSociConvertedIndex` looks.

## Q3. Do all original tags (`{commit}`, `build-NNN`, `latest`) move onto the index?

The spec notes the tag move is the one thing its benchmark never tested.

Run: `./verify/index_structure.sh <repo> <tag>` for each of the three tags, and
`aws ecr describe-images --repository-name <repo>` to compare digests.

- Pass: all three exit 0 and resolve to the same index digest, different from
  the original image digest.
- Fail obliges: stop; a partial move leaves customers deploying unconverted or
  inconsistent images. Fix the tag-move logic before shipping.

Result: PASS. `build-556`, `latest`, and `9dfa676b…` all resolve to index
`sha256:36db4409…`; the plain source digest `sha256:e2539e5d…` is untagged.
After the no-ARN test, `build-557` was restored to index `sha256:f0ae0011…`
with the same tag set.

## Q4. Does Fargate actually lazy-load it?

Run: an ECS task definition on Fargate platform 1.4.0 pointing at the
converted tag; run the task; compare image pull time from task lifecycle
timestamps (`pullStartedAt` / `pullStoppedAt` from `aws ecs describe-tasks`)
against the same image before conversion.

- Pass: pull time falls substantially (spec benchmark: ~29.6s to ~4.3s on a
  627 MiB image) and the task runs.
- Fail obliges: if the task fails to start, the index is not accepted by
  Fargate: stop. If it starts with no pull-time improvement, the index is not
  being used: the feature has no value as built; investigate before shipping.

Result: PASS. On Fargate platform 1.4.0, the release task pulled the SOCI index
in **5.8s**; the same image (build 557, plain, no-ARN) pulled in 39.1s. The
service tasks pulled in 10.5s (SOCI) vs 34.0s (plain), and all tasks started.
The spec's benchmark was 29.6s → 4.3s.

## Q5. Does a duplicate delivery of an already-converted image push anything?

The loop guard is the single most important correctness property. **No unit
test enforces it**: `shouldSkipConversion` is tested, but nothing fails if the
handler stops calling `IsConverted`, or if someone moves that call below `Pull`
(see the LOAD-BEARING ORDER comment in `source/handler.go`). Re-run it after
any change to the handler prologue.

The builder invokes once per build and there is no EventBridge rule, so tag
moves cannot re-enter conversion. This question covers duplicate deliveries:
a builder retry or a manual invoke.

Run after a successful conversion of an image tagged `{commit}`, `build-NNN`,
`latest`. Record `aws ecr describe-images --repository-name <repo>` as the
"before" state. Then invoke the function with `{"tag": "<commit-tag>"}`; the
tag now resolves to the converted index.

Expected response: `AlreadyConvertedMessage` ("Skipping image: already
converted to a SOCI index") with metric `reason: already-converted`, nil error.

The observables that prove nothing was pushed:

1. `describe-images` diff: **no tag changed digest, and no new manifest
   appears**.
2. CloudTrail shows **no `PutImage` event** from the invocation.

- Pass: the expected response and both observables.
- Fail obliges: if it converts again or anything is pushed, stop: this is the
  loop under a duplicate delivery. Depends on Q2; if Q2 failed, expect this to
  fail too. (The old "original digest" case is gone: the handler resolves the
  tag, and a tag cannot point at the original manifest after the move.)

Result: PASS. Invoking `{"tag": "build-556"}` returned
`"Skipping image: already converted to a SOCI index"` in 1.37s; the tag and
digest were unchanged, and no new manifest appeared. (CloudTrail was not
queried separately; the handler returns on the skip before any push.)

## Offline verdict logic

`verify/index_structure.sh --response <file>` takes a saved
`aws ecr batch-get-image` response, so the verdict logic can be run without
AWS. Run against the synthetic fixtures in `verify/fixtures/`:

| Fixture | Expected | Observed exit |
|---|---|---|
| synthetic-converted-index.json | 0 (pass) | 0 |
| synthetic-plain-index.json | 1 | 1 |
| synthetic-plain-manifest.json | 1 | 1 |
| synthetic-artifacttype-wrong-place.json | 1 | 1 |
| synthetic-malformed-manifest.json | 2 | 2 |
| synthetic-nonobject-manifest.json | 2 | 2 |
| synthetic-malformed-response.json | 2 | 2 |
| synthetic-unresolved.json | 2 | 2 |
