package v2

import (
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// =====================================================================
// DeleteObjects — multi-key delete (POST /{bucket}?delete)
// =====================================================================

func TestDeleteObjects(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("delete multiple keys", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		keep := h.put(t, bucket, "keep.txt", []byte("keep"), "")
		h.put(t, bucket, "a.txt", []byte("a"), "")
		h.put(t, bucket, "b.txt", []byte("b"), "")
		h.put(t, bucket, "c.txt", []byte("c"), "")

		h.rec.reset()
		out, err := h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{
				Objects: []types.ObjectIdentifier{
					{Key: aws.String("a.txt")},
					{Key: aws.String("b.txt")},
					{Key: aws.String("c.txt")},
				},
				Quiet: aws.Bool(false),
			},
		})
		if err != nil {
			failf(t, "DeleteObjects: %v", err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindXML)
		if len(out.Errors) != 0 {
			failf(t, "unexpected Errors: %+v", out.Errors)
		}
		deleted := map[string]bool{}
		for _, d := range out.Deleted {
			deleted[aws.ToString(d.Key)] = true
		}
		for _, k := range []string{"a.txt", "b.txt", "c.txt"} {
			if !deleted[k] {
				failf(t, "missing Deleted entry for %q (got %v)", k, deleted)
			}
		}

		// Deleted keys are gone; keep remains.
		for _, k := range []string{"a.txt", "b.txt", "c.txt"} {
			_, gerr := h.failing(t, func() error {
				_, err := h.client.GetObject(bg, &s3.GetObjectInput{
					Bucket: aws.String(bucket), Key: aws.String(k),
				})
				return err
			})
			_ = gerr
		}
		_, _, body := h.get(t, bucket, "keep.txt")
		if string(body) != "keep" {
			fail(t, "keep.txt was affected")
		}
		_ = keep
	})

	t.Run("quiet mode omits deleted list", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		h.put(t, bucket, "x", []byte("x"), "")
		h.put(t, bucket, "y", []byte("y"), "")

		out, err := h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{
				Objects: []types.ObjectIdentifier{
					{Key: aws.String("x")},
					{Key: aws.String("y")},
				},
				Quiet: aws.Bool(true),
			},
		})
		if err != nil {
			failf(t, "DeleteObjects quiet: %v", err)
		}
		if len(out.Deleted) != 0 {
			failf(t, "Quiet mode should omit Deleted, got %d entries", len(out.Deleted))
		}
		if len(out.Errors) != 0 {
			failf(t, "unexpected Errors: %+v", out.Errors)
		}
	})

	t.Run("missing keys are reported as deleted (idempotent)", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		out, err := h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{
				Objects: []types.ObjectIdentifier{
					{Key: aws.String("never-existed")},
				},
			},
		})
		if err != nil {
			failf(t, "DeleteObjects missing key: %v", err)
		}
		if len(out.Errors) != 0 {
			failf(t, "missing key must not be an Error, got %+v", out.Errors)
		}
		if len(out.Deleted) != 1 || aws.ToString(out.Deleted[0].Key) != "never-existed" {
			failf(t, "want Deleted=[never-existed], got %+v", out.Deleted)
		}
	})

	t.Run("mix of existing and missing keys", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		h.put(t, bucket, "exists", []byte("1"), "")
		out, err := h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{
				Objects: []types.ObjectIdentifier{
					{Key: aws.String("exists")},
					{Key: aws.String("ghost")},
				},
			},
		})
		if err != nil {
			failf(t, "DeleteObjects mix: %v", err)
		}
		if len(out.Errors) != 0 {
			failf(t, "unexpected Errors: %+v", out.Errors)
		}
		deleted := map[string]bool{}
		for _, d := range out.Deleted {
			deleted[aws.ToString(d.Key)] = true
		}
		if !deleted["exists"] || !deleted["ghost"] {
			failf(t, "Deleted = %v", deleted)
		}
	})

	t.Run("missing bucket", func(t *testing.T) {
		colorTestResult(t)
		name := uniqueBucket()
		ex, err := h.failing(t, func() error {
			_, err := h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
				Bucket: aws.String(name),
				Delete: &types.Delete{
					Objects: []types.ObjectIdentifier{{Key: aws.String("k")}},
				},
			})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "NoSuchBucket")
	})

	t.Run("empty object list is InvalidRequest", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &types.Delete{Objects: nil},
			})
			return err
		})
		// AWS returns MalformedXML / InvalidRequest depending on client payload.
		if ex.Status != http.StatusBadRequest {
			failf(t, "status %d, want 400", ex.Status)
		}
		_ = err
	})
}
