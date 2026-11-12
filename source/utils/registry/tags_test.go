// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"reflect"
	"testing"
)

func TestTagsToMoveFallsBackToEventTag(t *testing.T) {
	got := TagsToMove("deadbeef", nil)
	if want := []string{"deadbeef"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTagsToMoveIncludesEventTagMissingFromRegistryList(t *testing.T) {
	got := TagsToMove("deadbeef", []string{"latest"})
	if want := []string{"latest", "deadbeef"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTagsToMoveKeepsEveryRepositoryTag(t *testing.T) {
	got := TagsToMove("abc", []string{"abc", "build-7", "latest"})
	if want := []string{"abc", "build-7", "latest"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTagsToMoveDedupes(t *testing.T) {
	got := TagsToMove("latest", []string{"latest", "latest"})
	if want := []string{"latest"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
