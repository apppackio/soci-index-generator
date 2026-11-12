// Added by AppPack (see NOTICE.txt); not part of the upstream source.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
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

type fakeTagger struct {
	tags       map[string]string // tag -> digest
	resolveErr map[string]error
	tagErr     map[string]error
	tagged     []string
}

func (f *fakeTagger) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	if err := f.resolveErr[ref]; err != nil {
		return ocispec.Descriptor{}, err
	}
	d, ok := f.tags[ref]
	if !ok {
		return ocispec.Descriptor{}, fmt.Errorf("%s: %w", ref, errdef.ErrNotFound)
	}
	return ocispec.Descriptor{Digest: digest.Digest(d)}, nil
}

func (f *fakeTagger) Tag(ctx context.Context, desc ocispec.Descriptor, ref string) error {
	if err := f.tagErr[ref]; err != nil {
		return err
	}
	f.tagged = append(f.tagged, ref)
	return nil
}

// The logger reads the Lambda request id from the context.
func testCtx() context.Context {
	return lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{})
}

const srcDigest = "sha256:aaaa"

func TestTagAllMovesTagStillOnSourceDigest(t *testing.T) {
	f := &fakeTagger{tags: map[string]string{"abc": srcDigest, "latest": srcDigest}}
	if err := tagAll(testCtx(), f, ocispec.Descriptor{}, srcDigest, []string{"abc", "latest"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"abc", "latest"}; !reflect.DeepEqual(f.tagged, want) {
		t.Fatalf("tagged %v, want %v", f.tagged, want)
	}
}

func TestTagAllSkipsTagMovedToAnotherDigest(t *testing.T) {
	f := &fakeTagger{tags: map[string]string{"abc": srcDigest, "latest": "sha256:bbbb"}}
	if err := tagAll(testCtx(), f, ocispec.Descriptor{}, srcDigest, []string{"abc", "latest"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"abc"}; !reflect.DeepEqual(f.tagged, want) {
		t.Fatalf("tagged %v, want %v", f.tagged, want)
	}
}

func TestTagAllSkipsTagThatDoesNotResolve(t *testing.T) {
	f := &fakeTagger{tags: map[string]string{"abc": srcDigest}}
	if err := tagAll(testCtx(), f, ocispec.Descriptor{}, srcDigest, []string{"abc", "deleted"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"abc"}; !reflect.DeepEqual(f.tagged, want) {
		t.Fatalf("tagged %v, want %v", f.tagged, want)
	}
}

func TestTagAllMayLeaveNoTagsWhenEveryTagMovedOn(t *testing.T) {
	f := &fakeTagger{tags: map[string]string{"abc": "sha256:bbbb"}}
	if err := tagAll(testCtx(), f, ocispec.Descriptor{}, srcDigest, []string{"abc"}); err != nil {
		t.Fatal(err)
	}
	if len(f.tagged) != 0 {
		t.Fatalf("tagged %v, want none", f.tagged)
	}
}

func TestTagAllTagFailureNamesTagAndStops(t *testing.T) {
	f := &fakeTagger{
		tags:   map[string]string{"a": srcDigest, "b": srcDigest, "c": srcDigest},
		tagErr: map[string]error{"c": errors.New("boom")},
	}
	err := tagAll(testCtx(), f, ocispec.Descriptor{}, srcDigest, []string{"a", "b", "c"})
	if err == nil || !strings.Contains(err.Error(), `"c"`) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want error naming tag c, got %v", err)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(f.tagged, want) {
		t.Fatalf("tagged %v, want %v", f.tagged, want)
	}
}

func TestTagAllResolveFailureNamesTag(t *testing.T) {
	f := &fakeTagger{resolveErr: map[string]error{"a": errors.New("throttled")}}
	err := tagAll(testCtx(), f, ocispec.Descriptor{}, srcDigest, []string{"a"})
	if err == nil || !strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("want error naming tag a, got %v", err)
	}
	if len(f.tagged) != 0 {
		t.Fatalf("tagged %v, want none", f.tagged)
	}
}

func TestAnyTagResolvesToTrueWhenOneTagStillOnSource(t *testing.T) {
	f := &fakeTagger{tags: map[string]string{"abc": "sha256:bbbb", "latest": srcDigest}}
	ok, err := anyTagResolvesTo(testCtx(), f, srcDigest, []string{"abc", "latest"})
	if err != nil || !ok {
		t.Fatalf("got (%v, %v), want (true, nil)", ok, err)
	}
}

func TestAnyTagResolvesToFalseWhenEveryTagMovedOnOrGone(t *testing.T) {
	f := &fakeTagger{tags: map[string]string{"abc": "sha256:bbbb"}}
	ok, err := anyTagResolvesTo(testCtx(), f, srcDigest, []string{"abc", "deleted"})
	if err != nil || ok {
		t.Fatalf("got (%v, %v), want (false, nil)", ok, err)
	}
}

func TestAnyTagResolvesToReturnsResolveErrorNamingTag(t *testing.T) {
	f := &fakeTagger{resolveErr: map[string]error{"abc": errors.New("throttled")}}
	ok, err := anyTagResolvesTo(testCtx(), f, srcDigest, []string{"abc"})
	if err == nil || ok || !strings.Contains(err.Error(), `"abc"`) {
		t.Fatalf("got (%v, %v), want (false, error naming abc)", ok, err)
	}
}
