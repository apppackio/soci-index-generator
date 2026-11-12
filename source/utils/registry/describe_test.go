package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

type fakeDescribeAPI struct {
	out *ecr.DescribeImagesOutput
	err error
	in  *ecr.DescribeImagesInput
}

func (f *fakeDescribeAPI) DescribeImages(ctx context.Context, params *ecr.DescribeImagesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeImagesOutput, error) {
	f.in = params
	return f.out, f.err
}

func TestDescribeImageReturnsTagsAndSize(t *testing.T) {
	tags := []string{"abc123def456", "build-7", "latest"}
	api := &fakeDescribeAPI{out: &ecr.DescribeImagesOutput{ImageDetails: []types.ImageDetail{
		{ImageTags: tags, ImageSizeInBytes: aws.Int64(1_000)},
	}}}
	info, err := DescribeImage(context.Background(), api, "repo", "sha256:abcd")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Found || !info.SizeKnown || info.SizeBytes != 1_000 {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(info.Tags) != 3 || info.Tags[0] != tags[0] || info.Tags[1] != tags[1] || info.Tags[2] != tags[2] {
		t.Fatalf("tags = %v, want %v", info.Tags, tags)
	}
	if aws.ToString(api.in.RepositoryName) != "repo" || len(api.in.ImageIds) != 1 || aws.ToString(api.in.ImageIds[0].ImageDigest) != "sha256:abcd" {
		t.Fatalf("unexpected request: %+v", api.in)
	}
}

func TestDescribeImageReturnsNotFound(t *testing.T) {
	api := &fakeDescribeAPI{err: &types.ImageNotFoundException{}}
	info, err := DescribeImage(context.Background(), api, "repo", "sha256:abcd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Found {
		t.Fatalf("Found = true, want false")
	}
}

func TestDescribeImagePropagatesOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	api := &fakeDescribeAPI{err: boom}
	_, err := DescribeImage(context.Background(), api, "repo", "sha256:abcd")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestOversizedAtTheCeilingIsFalse(t *testing.T) {
	if (ImageInfo{Found: true, SizeBytes: 6_000_000_000, SizeKnown: true}).Oversized() {
		t.Fatal("exactly at the ceiling must not be oversized")
	}
}

func TestOversizedAboveTheCeilingIsTrue(t *testing.T) {
	if !(ImageInfo{Found: true, SizeBytes: 6_000_000_001, SizeKnown: true}).Oversized() {
		t.Fatal("above the ceiling must be oversized")
	}
}

func TestOversizedWhenSizeUnknownIsFalse(t *testing.T) {
	// An unknown size is not a zero size; the 15-minute timeout and the /tmp
	// free-space check remain the backstop.
	if (ImageInfo{Found: true, SizeKnown: false}).Oversized() {
		t.Fatal("unknown size must not be reported oversized")
	}
}
