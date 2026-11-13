// Added by AppPack (see NOTICE.txt); not part of the upstream source.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// MaxCompressedImageBytes is the largest image (sum of compressed layer sizes)
// we will convert. Lambda caps /tmp at 10 GB and runtime at 15 minutes.
const MaxCompressedImageBytes int64 = 6_000_000_000

// DescribeImagesAPI is the slice of the ECR client that DescribeImage needs.
type DescribeImagesAPI interface {
	DescribeImages(ctx context.Context, params *ecr.DescribeImagesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeImagesOutput, error)
}

// ImageInfo is what one DescribeImages call tells us about an image.
type ImageInfo struct {
	Found bool
	Tags  []string
	// Digest is the image's manifest digest. A tag lookup fills it in too.
	Digest string
	// SizeBytes is only meaningful when SizeKnown is true. ECR may omit it.
	SizeBytes int64
	SizeKnown bool
}

// NewEcrClient builds an ECR client, honouring ECR_ENDPOINT as authorizeEcr does.
func NewEcrClient(ctx context.Context) (*ecr.Client, error) {
	return newEcrClient(ctx)
}

// DescribeImage looks up an image by digest. A missing image is not an error:
// it returns ImageInfo{Found: false}.
func DescribeImage(ctx context.Context, api DescribeImagesAPI, repositoryName string, digest string) (ImageInfo, error) {
	return describeImage(ctx, api, repositoryName, types.ImageIdentifier{ImageDigest: &digest})
}

// DescribeImageByTag resolves a tag to its image details, including the digest.
// The builder sends only the tag to convert, so this is the handler's entry
// point. A missing tag is not an error: it returns ImageInfo{Found: false}.
func DescribeImageByTag(ctx context.Context, api DescribeImagesAPI, repositoryName string, tag string) (ImageInfo, error) {
	return describeImage(ctx, api, repositoryName, types.ImageIdentifier{ImageTag: &tag})
}

func describeImage(ctx context.Context, api DescribeImagesAPI, repositoryName string, identifier types.ImageIdentifier) (ImageInfo, error) {
	out, err := api.DescribeImages(ctx, &ecr.DescribeImagesInput{
		RepositoryName: &repositoryName,
		ImageIds:       []types.ImageIdentifier{identifier},
	})
	if err != nil {
		var notFound *types.ImageNotFoundException
		if errors.As(err, &notFound) {
			return ImageInfo{}, nil
		}
		return ImageInfo{}, err
	}
	if len(out.ImageDetails) == 0 {
		return ImageInfo{}, nil
	}
	d := out.ImageDetails[0]
	info := ImageInfo{Found: true, Tags: d.ImageTags}
	if d.ImageDigest != nil {
		info.Digest = *d.ImageDigest
	}
	if d.ImageSizeInBytes != nil {
		info.SizeBytes = *d.ImageSizeInBytes
		info.SizeKnown = true
	}
	return info, nil
}

// Oversized reports whether the image exceeds MaxCompressedImageBytes. It is a
// pure predicate. An unknown size is not a zero size: it returns false, and the
// caller must check SizeKnown and say the guard did not run.
func (i ImageInfo) Oversized() bool {
	return i.SizeKnown && i.SizeBytes > MaxCompressedImageBytes
}
