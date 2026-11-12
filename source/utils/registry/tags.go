// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package registry

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
