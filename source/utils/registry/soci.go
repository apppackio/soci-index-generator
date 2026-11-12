// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// SociIndexV2ArtifactType is the artifactType SOCI v2 stamps on the child
// manifest of an index it has converted.
const SociIndexV2ArtifactType = "application/vnd.amazon.soci.index.v2+json"

// IsSociConvertedIndex reports whether the given manifest is an image index
// that already carries a SOCI v2 index. It is the loop guard: moving tags onto
// a converted index raises new PUSH events, and this is how they are told
// apart from a build's push. It does no I/O.
func IsSociConvertedIndex(mediaType string, rawManifest []byte) (bool, error) {
	if !images.IsIndexType(mediaType) {
		return false, nil
	}
	var index ocispec.Index
	if err := json.Unmarshal(rawManifest, &index); err != nil {
		return false, fmt.Errorf("failed to parse image index: %w", err)
	}
	for _, m := range index.Manifests {
		if m.ArtifactType == SociIndexV2ArtifactType {
			return true, nil
		}
	}
	return false, nil
}

// IsConverted reports whether the manifest at digest is an index already
// converted to SOCI v2. Anything that is not an index is not converted.
func (registry *Registry) IsConverted(ctx context.Context, repositoryName string, digest string) (bool, error) {
	desc, err := registry.HeadManifest(ctx, repositoryName, digest)
	if err != nil {
		return false, err
	}
	if !images.IsIndexType(desc.MediaType) {
		return false, nil
	}
	repo, err := registry.registry.Repository(ctx, repositoryName)
	if err != nil {
		return false, err
	}
	_, rc, err := repo.FetchReference(ctx, digest)
	if err != nil {
		return false, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return false, err
	}
	return IsSociConvertedIndex(desc.MediaType, raw)
}
