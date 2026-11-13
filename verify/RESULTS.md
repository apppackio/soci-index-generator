# Live verification results

## STATUS: NOT RUN

**No check below has been executed against real AWS. Nothing in this file is a
result.** The author of this file had no AWS credentials, and creating
infrastructure in a real account is the repository owner's decision. Until a
human runs the steps and replaces each "NOT RUN" with recorded output, treat
every question as open. **Do not ship until all five are answered.**

The only thing that was actually run is the offline verdict logic of
`index_structure.sh` against hand-written synthetic fixtures (see
`verify/fixtures/synthetic-*.json`). Those fixtures are our own guess at the
structure, so they show the script's logic is right and show **nothing** about
what `soci.Convert()` really emits.

| Field | Value |
|---|---|
| Date run | NOT RUN |
| Region | NOT RUN |
| Account | NOT RUN |
| Image / repository | NOT RUN |
| Release zip version | NOT RUN |
| Run by | NOT RUN |

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

Result: NOT RUN.

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
NOT RUN. Paste the full output of `./verify/index_structure.sh ... 2>&1` here,
unedited. The `imageManifestMediaType:` line goes to stderr, so without `2>&1`
it is missing.
```

## Q3. Do all original tags (`{commit}`, `build-NNN`, `latest`) move onto the index?

The spec notes the tag move is the one thing its benchmark never tested.

Run: `./verify/index_structure.sh <repo> <tag>` for each of the three tags, and
`aws ecr describe-images --repository-name <repo>` to compare digests.

- Pass: all three exit 0 and resolve to the same index digest, different from
  the original image digest.
- Fail obliges: stop; a partial move leaves customers deploying unconverted or
  inconsistent images. Fix the tag-move logic before shipping.

Result: NOT RUN.

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

Result: NOT RUN.

## Q5. Does a tag move re-enter conversion, and are stale re-deliveries harmless?

The loop guard is the single most important correctness property. **No unit
test enforces it**: `shouldSkipConversion` is tested, but nothing fails if the
handler stops calling `IsConverted`, or if someone moves that call below `Pull`
(see the LOAD-BEARING ORDER comment in `source/handler.go`). This question is
the only regression net. Re-run it after any change to the handler prologue.

Run, after a successful conversion of an image tagged `{commit}`, `build-NNN`,
`latest`. First record `aws ecr describe-images --repository-name <repo>`
(every digest and its tags) as the "before" state. Then invoke the function
with a synthetic event for each of:

- (b) **the converted index's own digest** (with `image-tag` set to one of the
  moved tags). This is the event a tag move really produces, and the loop
  property that matters.
- (a) **the original image digest** (with the same `image-tag`). The original
  manifest is still a plain image manifest, so `IsConverted` is false and the
  loop guard does **not** fire for it; that is correct. It must instead skip
  because no tag still resolves to it (every tag now points at the index).

Expected responses:

| Event | Response | Metric `reason` |
|---|---|---|
| (b) index digest | `AlreadyConvertedMessage` ("Skipping image: already converted to a SOCI index") | `already-converted` |
| (a) original digest | `NoTagsResolveMessage` ("Skipping image: no candidate tag still points at this image") | `no-tags-resolve` |

Both return a nil error. The observable that proves there is no loop:

1. Run `aws ecr describe-images --repository-name <repo>` again and diff
   against the "before" state: **no tag changed digest, and no new manifest
   appears**.
2. CloudTrail shows **no `PutImage` event** from either invocation.

- Pass: both responses as in the table, and both observables hold.
- Fail obliges: if (b) does not return `AlreadyConvertedMessage` or anything
  is pushed, stop: this is the loop under production conditions. Depends on Q2;
  if Q2 failed, expect (b) to fail too. If only (a) differs (for example it
  converts again because a tag still resolves to the original), investigate the
  tag move (Q3) before touching the guard: (a) is not a guard question.

Result: NOT RUN.

## Offline verdict logic (the only thing actually run)

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
