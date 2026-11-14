package v2

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// =====================================================================
// SigV4 authentication
//
// Valid credentials (S3_ACCESS_KEY / S3_SECRET_KEY) are accepted.
// Bad or missing signatures must yield 403 AccessDenied — not silent
// success. This is required for a credible local S3.
// =====================================================================

func TestSigV4Auth(t *testing.T) {
	colorTestResult(t)

	t.Run("valid credentials succeed", func(t *testing.T) {
		colorTestResult(t)
		h := newHarness(t)
		bucket := h.newBucket(t)
		h.put(t, bucket, "ok.txt", []byte("auth-ok"), "text/plain")
		_, _, body := h.get(t, bucket, "ok.txt")
		if string(body) != "auth-ok" {
			fail(t, "authenticated round-trip failed")
		}
	})

	t.Run("wrong secret key is AccessDenied", func(t *testing.T) {
		colorTestResult(t)
		good := newHarness(t)
		bucket := good.newBucket(t)

		bad := newHarnessWith(t, harnessOpts{
			accessKey: accessKey(),
			secretKey: "definitely-not-the-secret",
		})
		ex, err := bad.failing(t, func() error {
			_, err := bad.client.PutObject(bg, &s3.PutObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String("nope.txt"),
				Body:   bytes.NewReader([]byte("x")),
			})
			return err
		})
		assertAPIError(t, ex, http.StatusForbidden, "AccessDenied", nil)
		requireSDKError(t, err, ex, "AccessDenied")

		// Object must not have been created.
		_, gerr := good.failing(t, func() error {
			_, err := good.client.GetObject(bg, &s3.GetObjectInput{
				Bucket: aws.String(bucket), Key: aws.String("nope.txt"),
			})
			return err
		})
		_ = gerr
	})

	t.Run("unknown access key is AccessDenied", func(t *testing.T) {
		colorTestResult(t)
		good := newHarness(t)
		bucket := good.newBucket(t)

		bad := newHarnessWith(t, harnessOpts{
			accessKey: "unknown-access-key",
			secretKey: secretKey(),
		})
		ex, err := bad.failing(t, func() error {
			_, err := bad.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
				Bucket: aws.String(bucket),
			})
			return err
		})
		assertAPIError(t, ex, http.StatusForbidden, "AccessDenied", nil)
		requireSDKError(t, err, ex, "AccessDenied")
	})

	t.Run("unsigned request is AccessDenied", func(t *testing.T) {
		colorTestResult(t)
		h := newHarness(t)
		bucket := h.newBucket(t)

		req, err := http.NewRequest(http.MethodGet, h.base+"/"+bucket+"?list-type=2", nil)
		if err != nil {
			fail(t, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			failf(t, "unsigned GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		ex := exchange{
			Method: "GET",
			Path:   "/" + bucket,
			Status: resp.StatusCode,
			Header: resp.Header.Clone(),
			Body:   body,
		}
		assertAPIError(t, ex, http.StatusForbidden, "AccessDenied", nil)
	})

	t.Run("wrong secret cannot delete bucket", func(t *testing.T) {
		colorTestResult(t)
		good := newHarness(t)
		bucket := good.newBucket(t)

		bad := newHarnessWith(t, harnessOpts{
			accessKey: accessKey(),
			secretKey: "wrong-secret",
		})
		ex, err := bad.failing(t, func() error {
			_, err := bad.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
			return err
		})
		assertAPIError(t, ex, http.StatusForbidden, "AccessDenied", nil)
		requireSDKError(t, err, ex, "AccessDenied")

		_, err = good.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
		if err != nil {
			failf(t, "bucket should still exist: %v", err)
		}
	})
}
