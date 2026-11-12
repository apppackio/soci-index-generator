// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws-ia/cfn-aws-soci-index-builder/soci-index-generator-lambda/events"
	"github.com/aws/aws-lambda-go/lambdacontext"
)

// This test ensures that the handler can pull Docker and OCI images, build, and push the SOCI index back to the repository.
// To run this test locally, you need to push an image to a private ECR repository, and set following environment variables:
// AWS_ACCOUNT_ID: your aws account id.
// AWS_REGION: the region of your private ECR repository.
// REPOSITORY_NAME: name of your private ECR repository.
// DOCKER_IMAGE_DIGEST: the digest of your image.
// OCI_IMAGE_DIGEST: the digest of your OCI image.
func TestHandlerHappyPath(t *testing.T) {
	// Test with both V1 and V2 SOCI index versions
	testVersions := []string{"V1", "V2"}

	for _, version := range testVersions {
		t.Run("SOCI Index Version "+version, func(t *testing.T) {
			// Set the SOCI index version environment variable
			if err := os.Setenv("soci_index_version", version); err != nil {
				t.Fatalf("Failed to set environment variable: %v", err)
			}
			t.Logf("Testing with SOCI index version: %s", version)

			doTest := func(imageDigest string) {
				event := events.ECRImageActionEvent{
					Version:    "1",
					Id:         "id",
					DetailType: "ECR Image Action",
					Source:     "aws.ecr",
					Account:    os.Getenv("AWS_ACCOUNT_ID"),
					Time:       "time",
					Region:     os.Getenv("AWS_REGION"),
					Detail: events.ECRImageActionEventDetail{
						ActionType:     "PUSH",
						Result:         "SUCCESS",
						RepositoryName: os.Getenv("REPOSITORY_NAME"),
						ImageDigest:    imageDigest,
						ImageTag:       "test",
					},
				}

				// making the test context
				lc := lambdacontext.LambdaContext{}
				lc.AwsRequestID = "request-id-" + imageDigest + "-" + version
				ctx := lambdacontext.NewContext(context.Background(), &lc)
				ctx, cancel := context.WithDeadline(ctx, time.Now().Add(time.Minute))
				defer cancel()

				resp, err := HandleRequest(ctx, event)
				if err != nil {
					t.Fatalf("HandleRequest failed with version %s: %v", version, err)
				}

				expected_resp := "Successfully built and pushed SOCI index"
				if resp != expected_resp {
					t.Fatalf("Unexpected response with version %s. Expected %s but got %s", version, expected_resp, resp)
				}
			}

			doTest(os.Getenv("DOCKER_IMAGE_DIGEST"))
			doTest(os.Getenv("OCI_IMAGE_DIGEST"))
		})
	}
}

// This test ensures that the handler can validate the input digest media type
// To run this test locally, you need to push an image to a private ECR repository, and set following environment variables:
// AWS_ACCOUNT_ID: your aws account id.
// AWS_REGION: the region of your private ECR repository.
// REPOSITORY_NAME: name of your private ECR repository.
// INVALID_IMAGE_DIGEST: the digest of anything that isn't an image.
func TestHandlerInvalidDigestMediaType(t *testing.T) {
	// Test with both V1 and V2 SOCI index versions
	testVersions := []string{"V1", "V2"}

	for _, version := range testVersions {
		t.Run("SOCI Index Version "+version, func(t *testing.T) {
			// Set the SOCI index version environment variable
			if err := os.Setenv("soci_index_version", version); err != nil {
				t.Fatalf("Failed to set environment variable: %v", err)
			}
			t.Logf("Testing with SOCI index version: %s", version)

			event := events.ECRImageActionEvent{
				Version:    "1",
				Id:         "id",
				DetailType: "ECR Image Action",
				Source:     "aws.ecr",
				Account:    os.Getenv("AWS_ACCOUNT_ID"),
				Time:       "time",
				Region:     os.Getenv("AWS_REGION"),
				Detail: events.ECRImageActionEventDetail{
					ActionType:     "PUSH",
					Result:         "SUCCESS",
					RepositoryName: os.Getenv("REPOSITORY_NAME"),
					ImageDigest:    os.Getenv("INVALID_IMAGE_DIGEST"),
					ImageTag:       "test",
				},
			}

			// making the test context
			lc := lambdacontext.LambdaContext{}
			lc.AwsRequestID = "abcd-1234-" + version
			ctx := lambdacontext.NewContext(context.Background(), &lc)
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(time.Minute))
			defer cancel()

			resp, err := HandleRequest(ctx, event)
			if err != nil {
				t.Fatalf("Invalid image digest is not expected to fail with version %s", version)
			}

			expected_resp := "Exited early due to manifest validation error"
			if resp != expected_resp {
				t.Fatalf("Unexpected response with version %s. Expected %s but got %s", version, expected_resp, resp)
			}
		})
	}
}
