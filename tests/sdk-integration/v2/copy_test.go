package v2

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// =====================================================================
// CopyObject — same-bucket and cross-bucket
// =====================================================================

func TestCopyObject(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("same bucket copy", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		body := []byte("copy-me-please")
		srcETag := h.put(t, bucket, "src/original.txt", body, "text/plain")

		h.rec.reset()
		out, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String("dst/copied.txt"),
			CopySource: aws.String(bucket + "/src/original.txt"),
		})
		if err != nil {
			failf(t, "CopyObject: %v", err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindXML)
		if aws.ToString(out.CopyObjectResult.ETag) == "" {
			fail(t, "CopyObjectResult.ETag missing")
		}

		gx, _, got := h.get(t, bucket, "dst/copied.txt")
		assertObjectHeaders(t, gx, len(body), srcETag, "text/plain")
		if !bytes.Equal(got, body) {
			fail(t, "copied body mismatch")
		}

		// Source untouched.
		_, _, srcBody := h.get(t, bucket, "src/original.txt")
		if !bytes.Equal(srcBody, body) {
			fail(t, "source mutated by copy")
		}
	})

	t.Run("cross bucket copy", func(t *testing.T) {
		colorTestResult(t)
		srcBucket := h.newBucket(t)
		dstBucket := h.newBucket(t)
		body := randBytes(64 << 10)
		etag := h.put(t, srcBucket, "file.bin", body, "application/octet-stream")

		_, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
			Bucket:     aws.String(dstBucket),
			Key:        aws.String("file.bin"),
			CopySource: aws.String(srcBucket + "/file.bin"),
		})
		if err != nil {
			failf(t, "CopyObject cross-bucket: %v", err)
		}
		ex, _, got := h.get(t, dstBucket, "file.bin")
		assertObjectHeaders(t, ex, len(body), etag, "application/octet-stream")
		if !bytes.Equal(got, body) {
			fail(t, "cross-bucket body mismatch")
		}
	})

	t.Run("copy replaces destination", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		h.put(t, bucket, "src", []byte("new-content"), "text/plain")
		h.put(t, bucket, "dst", []byte("old"), "text/plain")

		_, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String("dst"),
			CopySource: aws.String(bucket + "/src"),
		})
		if err != nil {
			failf(t, "CopyObject replace: %v", err)
		}
		_, _, got := h.get(t, bucket, "dst")
		if !bytes.Equal(got, []byte("new-content")) {
			failf(t, "dst body %q, want new-content", got)
		}
	})

	t.Run("copy preserves user metadata by default", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		h.putMeta(t, bucket, "src", []byte("x"), "text/plain", map[string]string{
			"color": "blue",
			"env":   "test",
		})
		_, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String("dst"),
			CopySource: aws.String(bucket + "/src"),
		})
		if err != nil {
			failf(t, "CopyObject: %v", err)
		}
		_, out := h.head(t, bucket, "dst")
		if out.Metadata["color"] != "blue" || out.Metadata["env"] != "test" {
			failf(t, "metadata not preserved: %v", out.Metadata)
		}
	})

	t.Run("missing source key", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String("dst"),
				CopySource: aws.String(bucket + "/nope"),
			})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchKey", map[string]string{"Key": "nope"})
		requireSDKError(t, err, ex, "NoSuchKey")
	})

	t.Run("missing source bucket", func(t *testing.T) {
		colorTestResult(t)
		dst := h.newBucket(t)
		missing := uniqueBucket()
		ex, err := h.failing(t, func() error {
			_, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
				Bucket:     aws.String(dst),
				Key:        aws.String("dst"),
				CopySource: aws.String(missing + "/k"),
			})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": missing})
		requireSDKError(t, err, ex, "NoSuchBucket")
	})

	t.Run("url-encoded source key with spaces", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		body := []byte("spaced")
		h.put(t, bucket, "my file.txt", body, "text/plain")
		_, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String("dest.txt"),
			CopySource: aws.String(bucket + "/my file.txt"),
		})
		if err != nil {
			failf(t, "CopyObject spaced key: %v", err)
		}
		_, _, got := h.get(t, bucket, "dest.txt")
		if !bytes.Equal(got, body) {
			fail(t, "spaced-key copy body mismatch")
		}
	})
}
