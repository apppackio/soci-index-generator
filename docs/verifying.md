# Verifying what runs in your account

The question this repo exists to answer: **is the code running in my AWS
account what this repository says it is?**

AppPack deploys a Lambda function into your account that has write access to
your container image repositories. The function is built from this repo. The
steps below let you check that yourself. Read the "proves / does not prove"
notes: each step answers a narrower question than it may appear to, and only
the last step connects the artifact to source.

## What you need

`aws` (with read access to your account), `gh`, `git`, `docker`, `sha256sum`,
`unzip`. Step 4 builds for `linux/amd64`; on an arm64 machine (Apple silicon,
Graviton) Docker emulates, so expect it to take minutes, not seconds.

## Step 1: ask Lambda what it is running

This is the strongest check in the procedure, because it does not trust S3.
Lambda reports `CodeSha256`, its own SHA-256 of the package it deployed.
Comparing that with the released zip's hash (step 3) verifies **what is
actually running**, whatever the bucket holds now or held when you deployed.

The CloudFormation logical ID of the function is fixed by AppPack's template:
`SociIndexGeneratorLambdaFunction`. The stack *name* is chosen by the AppPack
CLI and is not derivable from this repo, so supply your own app's stack name
(find it with `aws cloudformation list-stacks`).

```bash
aws cloudformation describe-stack-resources --stack-name <your app's stack> \
  --logical-resource-id SociIndexGeneratorLambdaFunction \
  --query 'StackResources[0].PhysicalResourceId' --output text   # (--query untested)

aws lambda get-function --function-name <that physical id> \
  --query Configuration.CodeSha256 --output text                # (--query untested)

# CodeSha256 is base64; convert to hex to compare with the release's hashes
echo '<CodeSha256 value>' | base64 -d | xxd -p -c 64
```

*Proves:* which package AWS says the function runs. Matched against the
release zip hash, whether that is the released zip.
*Does not prove:* anything about the source. It is a hash AWS reports about
the package it holds.

To learn which tag your account was deployed from (needed for steps 2 to 4),
read the version-pinned key, `soci-index-generator/<tag>/soci_index_generator_lambda.zip`,
from the stack template. The tag is its `vX.Y.Z` segment. (`get-function` does
not return the S3 key; it returns a presigned URL to Lambda's own copy.)

```bash
aws cloudformation get-template --stack-name <your app's stack> \
  --query TemplateBody --output text | grep -n 'soci-index-generator/'   # (untested)
```

## Step 2: download what your account's Lambda was built from

```bash
mkdir verify && cd verify
aws s3 cp s3://apppack-lambdas-<region>/soci-index-generator/<tag>/soci_index_generator_lambda.zip .
sha256sum soci_index_generator_lambda.zip
```

*Proves:* what the bucket holds under that key right now.
*Does not prove:* that it is the released artifact, or that it is what your
Lambda is running (the stack could have been updated since, or the bucket
could have been written by someone else). Step 1 covers the latter.

## Step 3: compare with the public GitHub release

Each release attaches the zip and two checksum files.

```bash
gh release download <tag> --repo apppackio/apppack-soci-index-generator \
  --pattern '*.sha256' --pattern 'soci_index_generator_lambda.zip' --dir release
cd release
sha256sum -c soci_index_generator_lambda.zip.sha256      # the release's own zip
cd ..
sha256sum release/soci_index_generator_lambda.zip soci_index_generator_lambda.zip   # S3 copy: both lines must match
```

*Proves:* the bucket holds the same bytes that were published on the release
(and, with step 1, that your Lambda runs them).
*Does not prove:* that those bytes were built from this source. A GitHub
release and an S3 object can both hold an artifact nobody can reproduce.
**Only step 4 ties the artifact to source.**

## Step 4: rebuild from source and compare

```bash
git clone https://github.com/apppackio/apppack-soci-index-generator
cd apppack-soci-index-generator
git checkout <tag>
make build checksums
cat dist/soci_index_generator_lambda.zip.sha256 dist/bootstrap.sha256
```

Compare with the release's files:

```bash
# the zip
diff dist/soci_index_generator_lambda.zip.sha256 ../verify/release/soci_index_generator_lambda.zip.sha256

# the binary: bootstrap.sha256 names a file inside the zip, not a loose file,
# so extract it first or `sha256sum -c` reports "No such file"
cd ../verify/release
unzip -o soci_index_generator_lambda.zip bootstrap
sha256sum -c bootstrap.sha256
```

## Which hash to trust: two anchors

Releases carry two hashes:

- **zip SHA-256**: exactly the bytes Lambda is given.
- **`bootstrap` SHA-256**: the compiled Go binary inside the zip, the thing
  that actually runs.

**The binary hash is the stronger signal.** The builder image is pinned by
digest, so the Go toolchain is fixed. But the `apk add` packages in that image
(`make`, `git`, `zip`, `gcc`, `musl-dev`, `zlib-dev`, `zlib-static`) are **not**
pinned. `gcc` and the static libraries are used to link the binary, so drift there
could change it too; the `zip` package only frames the archive. Concretely:

- Rebuilding soon after a release should reproduce both hashes. CI checks
  this on every push (a cached build, then a `--no-cache` build, compared) and
  the release workflow builds from the tag.
- Rebuilding **long after** a release may give a different *zip* hash with an
  **identical binary**, because the `zip` package moved. If `bootstrap`
  matches and the zip differs, the code is the same; the framing around it is
  not.
- If the **binary** differs, treat that as a real mismatch until explained.
  It may have a benign cause (an updated `gcc`, `musl-dev` or `zlib-static`
  among the unpinned packages), but a hash cannot tell a benign cause from a
  malicious one. Investigate: rebuild at the time of the release if you can,
  and ask.

**Reproducibility here is conditional, not absolute.** What CI shows is that,
at the moment it runs, a cold rebuild matches a cached build. It does not show
that the build will still reproduce next year. What a *match* proves is strong
(these bytes came from this source and this builder image); what a *mismatch*
proves is weak (something moved, possibly nothing sinister).

## What a full pass proves, and what it does not

A pass on all four steps, comparing the `bootstrap` hash, shows that the
binary inside the zip your Lambda runs was compiled from the source at that
tag, using the builder image named in `source/Dockerfile`.

It does **not** show:

- that the source is safe. You must read it, or trust a review of it. The
  diff against upstream is listed in `NOTICE.txt`; `source/vendor/` is
  byte-identical to upstream's.
- anything about the builder image beyond its digest, or the unpinned apk
  packages at the time of the original build.
- anything about the rest of your stack: the Lambda's IAM role, its
  environment, the EventBridge rule that triggers it. Those are defined in
  AppPack's CloudFormation templates, not here.
- that the commands in this document were run against a particular release
  by anyone but you.

## Releases are immutable by policy

Publishing writes to a version-pinned S3 key per region. The audit above is
only meaningful if a tag always means the same bytes, so:

**Published tags are never deleted and never re-pointed.** A fix ships as a
new tag.

This is a policy, not a technical guarantee. The release workflow makes the
common accident fail (`gh release create` refuses to create a release that
already exists, and the workflow does that before it touches S3). But a
maintainer who deleted a release and re-pushed the tag could re-run the
workflow, and the bytes under the S3 key would change without any error. If
you pinned a hash from an earlier look at a release, a changed hash for the
same tag is itself the finding.

## What was and was not tested when this was written

*As of 2026-11-12, at commit e65da78 (documentation added after it).*

This procedure was written before the first release existed. The checksum
commands (`make checksums`, `sha256sum -c`, `unzip -o ... bootstrap`) and the
`CodeSha256` base64-to-hex conversion were run against a local development
build. The steps that need a published release, a real S3 bucket or a deployed
stack (the `aws` and `gh release download` commands, including every `--query`
and grep marked untested above) had not been run. The statement that
`CodeSha256` is the base64 SHA-256 of the package is AWS's documented
behaviour, not something tested here. If one of those commands fails as
written, the document is wrong, not you.

**Exit condition:** once the first release has been verified end to end,
delete this section and record the result (date, tag, which hashes matched).
