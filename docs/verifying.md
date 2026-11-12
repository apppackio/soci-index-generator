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

## Step 1: find which release your Lambda was deployed from

The function comes from a version-pinned S3 key, `soci-index-generator/<tag>/soci_index_generator_lambda.zip`,
named in the template of your app's CloudFormation stack.

```bash
aws cloudformation describe-stack-resources --stack-name apppack-app-<name> \
  --query "StackResources[?ResourceType=='AWS::Lambda::Function'].[LogicalResourceId,PhysicalResourceId]" --output text

# the key (and so the tag) is in the stack template, under the function's Code
aws cloudformation get-template --stack-name apppack-app-<name> \
  --query TemplateBody --output text | grep -n 'soci-index-generator/'
```

The logical ID of the function begins with `SociIndexGenerator`; use its
physical ID in step 1b. The tag is the `vX.Y.Z` path segment of the key.

**Step 1b: record what Lambda itself reports.**

```bash
aws lambda get-function --function-name <physical-id> \
  --query Configuration.CodeSha256 --output text
```

`CodeSha256` is the SHA-256 of the deployment package, base64-encoded. Convert
it to hex to compare with the hashes below:

```bash
echo '<CodeSha256 value>' | base64 -d | xxd -p -c 64
```

*Proves:* which package AWS says the function runs, and, compared with the
release's zip hash in step 3, whether that is the released zip.
*Does not prove:* anything about the source. It is a hash AWS reports about
the package it holds.

## Step 2: download what your account's Lambda was built from

```bash
mkdir verify && cd verify
aws s3 cp s3://apppack-lambdas-<region>/soci-index-generator/<tag>/soci_index_generator_lambda.zip .
sha256sum soci_index_generator_lambda.zip
```

*Proves:* what the bucket holds under that key right now.
*Does not prove:* that it is the released artifact, or that it is what your
Lambda is running (the stack could have been updated since, or the bucket
could have been written by someone else). Step 1b covers the latter.

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
(and, with step 1b, that your Lambda runs them).
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

This procedure was written before the first release existed. The checksum
commands (`make checksums`, `sha256sum -c`, `unzip -o ... bootstrap`) and the
`CodeSha256` conversion were run against a local development build. The steps
that need a published release, a real S3 bucket or a deployed stack (the `aws`
and `gh release download` commands) had not been run against a real release.
If one of those commands fails as written, the document is wrong, not you.
