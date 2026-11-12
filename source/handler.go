// Modified by AppPack from the upstream original (see NOTICE.txt).
// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"errors"
	"path"

	"github.com/aws-ia/cfn-aws-soci-index-builder/soci-index-generator-lambda/events"
	"github.com/aws-ia/cfn-aws-soci-index-builder/soci-index-generator-lambda/utils/fs"
	"github.com/aws-ia/cfn-aws-soci-index-builder/soci-index-generator-lambda/utils/log"
	"github.com/aws-ia/cfn-aws-soci-index-builder/soci-index-generator-lambda/utils/metrics"
	registryutils "github.com/aws-ia/cfn-aws-soci-index-builder/soci-index-generator-lambda/utils/registry"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/containerd/containerd/images"
	"oras.land/oras-go/v2/content/oci"

	"github.com/awslabs/soci-snapshotter/soci"
	"github.com/awslabs/soci-snapshotter/soci/store"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/platforms"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ErrEmptyIndex is now exported by the SOCI library, but we keep our own definition for backward compatibility
var (
	ErrEmptyIndex = errors.New("no ztocs created, all layers either skipped or produced errors")
)

const (
	BuildFailedMessage           = "SOCI index build error"
	TagFailedMessage             = "SOCI V2 OCI Image tag error"
	PushFailedMessage            = "SOCI index push error"
	SkipPushOnEmptyIndexMessage  = "Skipping pushing SOCI index as it does not contain any zTOCs"
	BuildAndPushSuccessMessage   = "Successfully built and pushed SOCI index"
	AlreadyConvertedMessage      = "Skipping image: already converted to a SOCI index"
	ConversionCheckFailedMessage = "Skipping image: conversion check failed, image left unconverted"
	DescribeFailedMessage        = "ECR DescribeImages error"
	ImageGoneMessage             = "Skipping image: no longer present in the repository"
	ValidationFailedMessage      = "ECRImageActionEvent validation error"
	InvalidIndexVersionMessage   = "Invalid soci_index_version configuration"
	NoTagsResolveMessage         = "Skipping image: no candidate tag still points at this image"
	OversizedMessage             = "Skipping image: compressed size exceeds the conversion ceiling"

	// Reasons for failures that do not pass through lambdaError.
	TimeoutReason = "invocation-timeout"
	PanicReason   = "panic"

	artifactsStoreName = "store"
	artifactsDbName    = "artifacts.db"
)

// Define custom context key types to avoid collisions
type contextKey string

const (
	RegistryURLKey     contextKey = "RegistryURL"
	RepositoryNameKey  contextKey = "RepositoryName"
	ImageDigestKey     contextKey = "ImageDigest"
	ImageTagKey        contextKey = "ImageTag"
	SOCIIndexDigestKey contextKey = "SOCIIndexDigest"
	SociIndexVersion   string     = "soci_index_version"
)

// invocation carries what the metric for one HandleRequest call needs. Every exit
// path ends in exactly one finish call.
type invocation struct {
	out        io.Writer
	app        string
	start      time.Time
	repository string
	digest     string
	// sizeGuardSkipped records that ECR reported no image size, so the size
	// ceiling did not run. It rides on whatever metric this invocation emits.
	sizeGuardSkipped bool
	// emitted makes the first finish win, so a timeout or panic that follows an
	// already-reported exit cannot count the same invocation twice.
	emitted atomic.Bool
}

// newInvocation never lets App be empty: CloudWatch silently drops an EMF
// document whose dimension value is an empty string, so a missing APP_NAME would
// discard every metric with no error anywhere. Each AppPack app owns exactly one
// ECR repository, so the repository name identifies it just as well.
func newInvocation(out io.Writer, app, repository, digest string) *invocation {
	if app == "" {
		app = repository
	}
	if app == "" {
		app = "unknown"
	}
	return &invocation{out: out, app: app, start: time.Now(), repository: repository, digest: digest}
}

// finish emits the invocation's metric. The error is deliberately dropped: a
// metrics failure must never change the handler's result, since the worst
// acceptable outcome is a slow task start, not a lost index.
//
// reason must be a bounded, stable value (a message constant) so outcomes
// aggregate; the unbounded raw error text goes in the separate error property.
func (inv *invocation) finish(outcome, reason string, err error) {
	if !inv.emitted.CompareAndSwap(false, true) {
		return
	}
	props := map[string]string{"repository": inv.repository, "digest": inv.digest}
	if reason != "" {
		props["reason"] = reason
	}
	if err != nil {
		props["error"] = err.Error()
	}
	if inv.sizeGuardSkipped {
		props["size_guard"] = "not-enforced"
	}
	_ = metrics.Emit(inv.out, inv.app, outcome, time.Since(inv.start), props)
}

// skip emits a skipped metric and returns the message with a nil error, so
// Lambda does not retry.
func (inv *invocation) skip(reason, msg string) (string, error) {
	inv.finish(metrics.OutcomeSkipped, reason, nil)
	return msg, nil
}

// fail emits a failure metric and returns the error to Lambda.
func (inv *invocation) fail(ctx context.Context, msg string, err error) (string, error) {
	inv.finish(metrics.OutcomeFailure, msg, err)
	return lambdaError(ctx, msg, err)
}

func HandleRequest(ctx context.Context, event events.ECRImageActionEvent) (string, error) {
	inv := newInvocation(os.Stdout, os.Getenv("APP_NAME"), event.Detail.RepositoryName, event.Detail.ImageDigest)

	// A panic unwinds past every exit path below. Report it, then re-panic:
	// swallowing it would turn a crash into a silent success.
	defer func() {
		if r := recover(); r != nil {
			inv.finish(metrics.OutcomeFailure, PanicReason, fmt.Errorf("%v", r))
			// Re-panicking replaces the stack with this site; keep the original.
			// Plain stderr, not log.Error: the logger needs a Lambda context and a
			// failure here would mask the panic being reported.
			fmt.Fprintf(os.Stderr, "panic: %v\n%s\n", r, debug.Stack())
			panic(r)
		}
	}()

	ctx, err := validateEvent(ctx, event)
	if err != nil {
		return inv.fail(ctx, ValidationFailedMessage, err)
	}

	sociIndexVersion, err := resolveIndexVersion(os.Getenv(SociIndexVersion))
	if err != nil {
		return inv.fail(ctx, InvalidIndexVersionMessage, err)
	}
	log.Info(ctx, fmt.Sprintf("Using SOCI index version: %s", sociIndexVersion))

	repo := event.Detail.RepositoryName
	digest := event.Detail.ImageDigest
	registryUrl := buildEcrRegistryUrl(event)
	ctx = context.WithValue(ctx, RegistryURLKey, registryUrl)

	registry, err := registryutils.Init(ctx, registryUrl)
	if err != nil {
		return inv.fail(ctx, "Remote registry initialization error", err)
	}

	// Loop guard: moving tags onto a converted index raises new PUSH events that
	// must not re-enter conversion. Cheapest and most dangerous check, so first.
	//
	// LOAD-BEARING ORDER: this call must stay here, before DescribeImage and long
	// before Pull. NO TEST ENFORCES THIS. shouldSkipConversion is unit tested, but
	// nothing fails if the handler stops calling IsConverted or someone moves it
	// below Pull. The only regression net is live question Q5 in verify/RESULTS.md.
	// If you touch this prologue, re-run Q5.
	converted, err := registry.IsConverted(ctx, repo, digest)
	if shouldSkipConversion(converted, err) {
		if err != nil {
			log.Warn(ctx, fmt.Sprintf("%s: %v", ConversionCheckFailedMessage, err))
			return inv.skip("conversion-check-failed", ConversionCheckFailedMessage)
		}
		log.Info(ctx, AlreadyConvertedMessage)
		return inv.skip("already-converted", AlreadyConvertedMessage)
	}

	// Size ceiling. Each skip returns a nil error so Lambda does not retry.
	ecrClient, err := registryutils.NewEcrClient(ctx)
	if err != nil {
		return inv.fail(ctx, DescribeFailedMessage, err)
	}
	info, err := registryutils.DescribeImage(ctx, ecrClient, repo, digest)
	if err != nil {
		return inv.fail(ctx, DescribeFailedMessage, err)
	}
	if !info.Found {
		return inv.skip("image-gone", ImageGoneMessage)
	}
	if !info.SizeKnown {
		inv.sizeGuardSkipped = true
		log.Warn(ctx, "ECR did not report an image size, so the size ceiling was NOT enforced: the size guard did not run")
	}
	if info.Oversized() {
		log.Warn(ctx, fmt.Sprintf("%s: %d bytes", OversizedMessage, info.SizeBytes))
		return inv.skip("oversized", OversizedMessage)
	}

	err = registry.ValidateImageDigest(ctx, repo, digest, sociIndexVersion)
	if err != nil {
		log.Warn(ctx, fmt.Sprintf("Image manifest validation error: %v", err))
		// Returning a non error to skip retries
		return inv.skip("manifest-validation", "Exited early due to manifest validation error")
	}

	// Only convert images that have a tag, and move those tags onto the new index.
	originalTag, ok := ctx.Value(ImageTagKey).(string)
	if !ok || originalTag == "" {
		log.Info(ctx, "Skipping SOCI index generation for V2 as image has no tag")
		return inv.skip("no-tag", "Skipped SOCI index generation for V2 as image has no tag")
	}

	// The converted index takes over every tag on the source digest, so
	// workloads deploying {repo}:{tag} get lazy loading.
	tags := registryutils.TagsToMove(originalTag, info.Tags)
	log.Info(ctx, fmt.Sprintf("Candidate tags to move onto the converted index: %v", tags))

	// Resolve before spending minutes of zTOC work. If no candidate still points
	// at this digest (duplicate delivery, late retry, a newer build took the
	// tag), the converted index would be pushed untagged and nothing would ever
	// reference it, pinning its layers until the lifecycle rule reaps it.
	anyResolves, err := registry.AnyTagResolvesTo(ctx, repo, digest, tags)
	if err != nil {
		return inv.fail(ctx, "Tag resolution error", err)
	}
	if !anyResolves {
		log.Info(ctx, NoTagsResolveMessage)
		return inv.skip("no-tags-resolve", NoTagsResolveMessage)
	}

	// Directory in lambda storage to store images and SOCI artifacts
	dataDir, err := createTempDir(ctx)
	if err != nil {
		return inv.fail(ctx, "Directory create error", err)
	}
	defer cleanUp(ctx, dataDir)

	// The channel to signal the deadline monitor goroutine to exit early.
	// Buffered so the deferred send never blocks when that goroutine has already
	// returned on timeout (a blocked defer would also stop cleanUp from running).
	quitChannel := make(chan int, 1)
	defer func() {
		quitChannel <- 1
	}()

	setDeadline(ctx, quitChannel, dataDir, inv)

	sociStore, err := initSociStore(ctx, dataDir)
	if err != nil {
		return inv.fail(ctx, "OCI storage initialization error", err)
	}

	desc, err := registry.Pull(ctx, repo, sociStore, digest)
	if err != nil {
		return inv.fail(ctx, "Image pull error", err)
	}

	image := images.Image{
		Name:   repo + "@" + digest,
		Target: *desc,
	}

	indexDescriptor, err := buildIndex(ctx, dataDir, sociStore, image, sociIndexVersion)
	if err != nil {
		if err.Error() == ErrEmptyIndex.Error() {
			log.Warn(ctx, SkipPushOnEmptyIndexMessage)
			return inv.skip("empty-index", SkipPushOnEmptyIndexMessage)
		}
		return inv.fail(ctx, BuildFailedMessage, err)
	}

	ctx = context.WithValue(ctx, SOCIIndexDigestKey, indexDescriptor.Digest.String())

	err = registry.Push(ctx, sociStore, *indexDescriptor, repo, digest, tags)
	if err != nil {
		return inv.fail(ctx, PushFailedMessage, err)
	}

	log.Info(ctx, BuildAndPushSuccessMessage)
	inv.finish(metrics.OutcomeSuccess, "", nil)
	return BuildAndPushSuccessMessage, nil
}

// resolveIndexVersion enforces SOCI v2, the only version this function supports
// (new Fargate customers cannot use v1). Unset means V2. Anything else is a
// configuration error and fails the invocation: a silent fallback to the V1 path
// would push untagged indexes Fargate ignores while the metric said success.
func resolveIndexVersion(raw string) (string, error) {
	switch raw {
	case "", "V2":
		return "V2", nil
	}
	return "", fmt.Errorf("%s must be unset or exactly %q, got %q: only SOCI v2 is supported", SociIndexVersion, "V2", raw)
}

// shouldSkipConversion reports whether to leave the image alone.
// It fails closed: a failed check skips. If a persistent registry error (missing
// IAM permission, throttling, outage) let conversion proceed, we would convert an
// already-converted index, move its tags, and raise fresh PUSH events that error
// the same way: an unbounded loop against ECR in a customer's account. Skipping
// costs one image its lazy loading; looping is a billing incident.
func shouldSkipConversion(converted bool, checkErr error) bool {
	return checkErr != nil || converted
}

// Validate the given event, populating the context with relevant valid event properties
func validateEvent(ctx context.Context, event events.ECRImageActionEvent) (context.Context, error) {
	var errors []error

	if event.Source != "aws.ecr" {
		errors = append(errors, fmt.Errorf("the event's 'source' must be 'aws.ecr'"))
	}
	if event.Account == "" {
		errors = append(errors, fmt.Errorf("the event's 'account' must not be empty"))
	}
	if event.DetailType != "ECR Image Action" {
		errors = append(errors, fmt.Errorf("the event's 'detail-type' must be 'ECR Image Action'"))
	}
	if event.Detail.ActionType != "PUSH" {
		errors = append(errors, fmt.Errorf("the event's 'detail.action-type' must be 'PUSH'"))
	}
	if event.Detail.Result != "SUCCESS" {
		errors = append(errors, fmt.Errorf("the event's 'detail.result' must be 'SUCCESS'"))
	}
	if event.Detail.RepositoryName == "" {
		errors = append(errors, fmt.Errorf("the event's 'detail.repository-name' must not be empty"))
	}
	if event.Detail.ImageDigest == "" {
		errors = append(errors, fmt.Errorf("the event's 'detail.image-digest' must not be empty"))
	}

	validAccountId, err := regexp.MatchString(`[0-9]{12}`, event.Account)
	if err != nil {
		errors = append(errors, err)
	}
	if !validAccountId {
		errors = append(errors, fmt.Errorf("the event's 'account' must be a valid AWS account ID"))
	}

	validRepositoryName, err := regexp.MatchString(`(?:[a-z0-9]+(?:[._-][a-z0-9]+)*/)*[a-z0-9]+(?:[._-][a-z0-9]+)*`, event.Detail.RepositoryName)
	if err != nil {
		errors = append(errors, err)
	}
	if validRepositoryName {
		ctx = context.WithValue(ctx, RepositoryNameKey, event.Detail.RepositoryName)
	} else {
		errors = append(errors, fmt.Errorf("the event's 'detail.repository-name' must be a valid repository name"))
	}

	validImageDigest, err := regexp.MatchString(`[[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*[:][A-Fa-f0-9]{32,}`, event.Detail.ImageDigest)
	if err != nil {
		errors = append(errors, err)
	}
	if validImageDigest {
		ctx = context.WithValue(ctx, ImageDigestKey, event.Detail.ImageDigest)
	} else {
		errors = append(errors, fmt.Errorf("the event's 'detail.image-digest' must be a valid image digest"))
	}

	// missing/empty tag is OK
	if event.Detail.ImageTag != "" {
		validImageTag, err := regexp.MatchString(`[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}`, event.Detail.ImageTag)
		if err != nil {
			errors = append(errors, err)
		}
		if validImageTag {
			ctx = context.WithValue(ctx, ImageTagKey, event.Detail.ImageTag)
		} else {
			errors = append(errors, fmt.Errorf("the event's 'detail.image-tag' must be empty or a valid image tag"))
		}
	}

	if len(errors) == 0 {
		return ctx, nil
	} else {
		return ctx, errors[0]
	}
}

// Returns ecr registry url from an image action event
func buildEcrRegistryUrl(event events.ECRImageActionEvent) string {
	var awsDomain = ".amazonaws.com"
	if strings.HasPrefix(event.Region, "cn") {
		awsDomain = ".amazonaws.com.cn"
	}
	return event.Account + ".dkr.ecr." + event.Region + awsDomain
}

// Create a temp directory in /tmp
// The directory is prefixed by the Lambda's request id
func createTempDir(ctx context.Context) (string, error) {
	// free space in bytes
	freeSpace := fs.CalculateFreeSpace("/tmp")
	log.Info(ctx, fmt.Sprintf("There are %d bytes of free space in /tmp directory", freeSpace))
	if freeSpace < 6_000_000_000 {
		// this is problematic because we support images as big as 6GB
		log.Warn(ctx, fmt.Sprintf("Free space in /tmp is only %d bytes, which is less than 6GB", freeSpace))
	}

	log.Info(ctx, "Creating a directory to store images and SOCI artifacts")
	lambdaContext, _ := lambdacontext.FromContext(ctx)
	tempDir, err := os.MkdirTemp("/tmp", lambdaContext.AwsRequestID) // The temp dir name is prefixed by the request id
	return tempDir, err
}

// Clean up the data written by the Lambda
func cleanUp(ctx context.Context, dataDir string) {
	log.Info(ctx, fmt.Sprintf("Removing all files in %s", dataDir))
	if err := os.RemoveAll(dataDir); err != nil {
		log.Error(ctx, "Clean up error", err)
	}
}

// Set up deadline for the lambda to proactively clean up its data before the invocation timeout. We don't
// want to keep data in storage when the Lambda reaches its invocation timeout.
// This function creates a goroutine that will do cleanup when the invocation timeout is near.
// quitChannel is used for signaling that goroutine when the invocation ends naturally.
func setDeadline(ctx context.Context, quitChannel chan int, dataDir string, inv *invocation) {
	// setting deadline as 10 seconds before lambda timeout.
	// reference: https://docs.aws.amazon.com/lambda/latest/dg/golang-context.html
	deadline, _ := ctx.Deadline()
	deadline = deadline.Add(-10 * time.Second)
	timeoutChannel := time.After(time.Until(deadline))
	go func() {
		for {
			select {
			case <-timeoutChannel:
				cleanUp(ctx, dataDir)
				timeoutErr := fmt.Errorf("invocation timeout after 14 minutes and 50 seconds")
				inv.finish(metrics.OutcomeFailure, TimeoutReason, timeoutErr)
				log.Error(ctx, "Invocation timeout error", timeoutErr)
				return
			case <-quitChannel:
				return
			}
		}
	}()
}

// Init containerd store
func initContainerdStore(dataDir string) (content.Store, error) {
	containerdStore, err := local.NewStore(path.Join(dataDir, artifactsStoreName))
	return containerdStore, err
}

// Init SOCI artifact store
func initSociStore(ctx context.Context, dataDir string) (*store.SociStore, error) {
	// Note: We are wrapping an *oci.Store in a store.SociStore because soci.WriteSociIndex
	// expects a store.Store, an interface that extends the oci.Store to provide support
	// for garbage collection.
	ociStore, err := oci.NewWithContext(ctx, path.Join(dataDir, artifactsStoreName))
	return &store.SociStore{
		Store: ociStore,
	}, err
}

// Init a new instance of SOCI artifacts DB
func initSociArtifactsDb(dataDir string) (*soci.ArtifactsDb, error) {
	artifactsDbPath := path.Join(dataDir, artifactsDbName)
	artifactsDb, err := soci.NewDB(artifactsDbPath)
	if err != nil {
		return nil, err
	}
	return artifactsDb, nil
}

// Build soci index for an image and returns its ocispec.Descriptor
func buildIndex(ctx context.Context, dataDir string, sociStore *store.SociStore, image images.Image, sociIndexVersion string) (*ocispec.Descriptor, error) {
	log.Info(ctx, "Building SOCI index")
	platform := platforms.DefaultSpec() //nolint:staticcheck // TODO: migrate to new API when available

	artifactsDb, err := initSociArtifactsDb(dataDir)
	if err != nil {
		return nil, err
	}

	containerdStore, err := initContainerdStore(dataDir)
	if err != nil {
		return nil, err
	}
	builderOpts := []soci.BuilderOption{
		soci.WithBuildToolIdentifier("AWS SOCI Index Builder Cfn v0.2"),
		soci.WithArtifactsDb(artifactsDb),
	}

	builder, err := soci.NewIndexBuilder(containerdStore, sociStore, builderOpts...)
	if err != nil {
		return nil, err
	}

	// Build the SOCI index based on the specified version
	if sociIndexVersion == "V2" {
		// Use Convert() for V2 index generation
		platforms, err := images.Platforms(ctx, containerdStore, image.Target)
		if err != nil {
			return nil, err
		}
		convertedOCIIndex, err := builder.Convert(ctx, image, soci.ConvertWithPlatforms(platforms...))
		if err != nil {
			return nil, fmt.Errorf("failed to convert OCI index: %w", err)
		}
		fmt.Printf("Generated OCI Index Digest: %s\n", convertedOCIIndex.Digest.String())
		return convertedOCIIndex, nil
	} else {
		// Default to Build() for V1 index generation
		generatedSOCIIndex, err := builder.Build(ctx, image, soci.WithPlatform(platform))
		if err != nil {
			return nil, fmt.Errorf("failed to build SOCI index: %w", err)
		}
		fmt.Printf("Generated SOCI Index Digest: %s\n", generatedSOCIIndex.ImageDesc.Digest.String())
		// Get SOCI indices for the image from the OCI store
		indexDescriptorInfos, _, err := soci.GetIndexDescriptorCollection(ctx, containerdStore, artifactsDb, image, []ocispec.Platform{platform})
		if err != nil {
			return nil, err
		}
		if len(indexDescriptorInfos) == 0 {
			return nil, errors.New("no SOCI indices found in OCI store")
		}
		sort.Slice(indexDescriptorInfos, func(i, j int) bool {
			return indexDescriptorInfos[i].CreatedAt.Before(indexDescriptorInfos[j].CreatedAt)
		})

		return &indexDescriptorInfos[len(indexDescriptorInfos)-1].Descriptor, nil
	}
}

// Log and return the lambda handler error
func lambdaError(ctx context.Context, msg string, err error) (string, error) {
	log.Error(ctx, msg, err)
	return msg, err
}

func main() {
	lambda.Start(HandleRequest)
}
