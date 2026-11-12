// Added by AppPack (see NOTICE.txt); not part of the upstream source.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws-ia/cfn-aws-soci-index-builder/soci-index-generator-lambda/utils/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
)

// tagger is the slice of an oras repository that tagAll needs.
type tagger interface {
	Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error)
	Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error
}

// TagsToMove returns every tag the converted index must take over: the tags
// ECR reports on the source digest, plus the event's tag if ECR did not list it
// (ECR is eventually consistent). Order is preserved and duplicates dropped.
//
// It must be all of them, not just the event tag. An AppPack build tags one
// image {commit}, build-NNN and latest; leaving any behind on the unconverted
// manifest breaks the lifecycle policy count and the commit lookup that reads
// tags co-located on build-NNN.
func TagsToMove(eventTag string, repositoryTags []string) []string {
	seen := make(map[string]bool, len(repositoryTags)+1)
	tags := make([]string, 0, len(repositoryTags)+1)
	for _, t := range append(append([]string{}, repositoryTags...), eventTag) {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	return tags
}

// tagAll points each tag at desc, but only if the tag still resolves to
// sourceDigest. The tag list was read before a conversion that takes minutes, so
// a newer build may have taken a tag (typically latest) in the meantime:
//   - resolves to sourceDigest: move it
//   - resolves to another digest: skip, a newer build owns it
//   - does not resolve: skip, we move tags and never resurrect deleted ones
//
// This narrows the race from minutes to milliseconds (a build can still take a
// tag between our Resolve and Tag); it does not close it. A failure on any tag
// stops and returns an error naming it.
func tagAll(ctx context.Context, t tagger, desc ocispec.Descriptor, sourceDigest string, tags []string) error {
	for _, tag := range tags {
		current, err := t.Resolve(ctx, tag)
		if errors.Is(err, errdef.ErrNotFound) {
			log.Warn(ctx, fmt.Sprintf("Not tagging index with %s: tag no longer exists", tag))
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to resolve tag %q before moving it: %w", tag, err)
		}
		if current.Digest.String() != sourceDigest {
			log.Warn(ctx, fmt.Sprintf("Not tagging index with %s: tag now points at %s, not the converted image %s", tag, current.Digest, sourceDigest))
			continue
		}
		log.Info(ctx, fmt.Sprintf("Tagging index with %s", tag))
		if err := t.Tag(ctx, desc, tag); err != nil {
			return fmt.Errorf("failed to tag artifact with %q: %w", tag, err)
		}
	}
	return nil
}

// anyTagResolvesTo reports whether at least one tag currently resolves to
// sourceDigest. It uses the same tagger seam and the same not-found handling as
// tagAll. An error other than not-found is returned: the caller cannot tell
// whether the tags still point at the source, so it must not guess.
func anyTagResolvesTo(ctx context.Context, t tagger, sourceDigest string, tags []string) (bool, error) {
	for _, tag := range tags {
		current, err := t.Resolve(ctx, tag)
		if errors.Is(err, errdef.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("failed to resolve tag %q: %w", tag, err)
		}
		if current.Digest.String() == sourceDigest {
			return true, nil
		}
	}
	return false, nil
}
