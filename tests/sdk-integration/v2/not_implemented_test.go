package v2

import (
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// =====================================================================
// Explicit NotImplemented — unsupported S3 features must fail clearly,
// never silently succeed while ignoring the requested feature.
// =====================================================================

func TestNotImplemented(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	h.put(t, bucket, "obj.txt", []byte("x"), "text/plain")

	cases := []struct {
		name string
		op   func() error
	}{
		{
			name: "GetBucketAcl",
			op: func() error {
				_, err := h.client.GetBucketAcl(bg, &s3.GetBucketAclInput{Bucket: aws.String(bucket)})
				return err
			},
		},
		{
			name: "GetObjectTagging",
			op: func() error {
				_, err := h.client.GetObjectTagging(bg, &s3.GetObjectTaggingInput{
					Bucket: aws.String(bucket), Key: aws.String("obj.txt"),
				})
				return err
			},
		},
		{
			name: "PutObjectTagging",
			op: func() error {
				_, err := h.client.PutObjectTagging(bg, &s3.PutObjectTaggingInput{
					Bucket: aws.String(bucket),
					Key:    aws.String("obj.txt"),
					Tagging: &types.Tagging{
						TagSet: []types.Tag{{
							Key:   aws.String("env"),
							Value: aws.String("test"),
						}},
					},
				})
				return err
			},
		},
		{
			name: "GetBucketVersioning",
			op: func() error {
				_, err := h.client.GetBucketVersioning(bg, &s3.GetBucketVersioningInput{
					Bucket: aws.String(bucket),
				})
				return err
			},
		},
		{
			name: "GetBucketLifecycleConfiguration",
			op: func() error {
				_, err := h.client.GetBucketLifecycleConfiguration(bg, &s3.GetBucketLifecycleConfigurationInput{
					Bucket: aws.String(bucket),
				})
				return err
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			colorTestResult(t)
			ex, err := h.failing(t, c.op)
			assertAPIError(t, ex, http.StatusNotImplemented, "NotImplemented", nil)
			requireSDKError(t, err, ex, "NotImplemented")
		})
	}
}
