package v2

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// =====================================================================
// User metadata — x-amz-meta-*
// =====================================================================

func TestObjectUserMetadata(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)

	t.Run("put get head round-trip", func(t *testing.T) {
		colorTestResult(t)
		meta := map[string]string{
			"author":  "raman",
			"project": "owns3",
			"stage":   "v2",
		}
		h.putMeta(t, bucket, "meta.txt", []byte("hello"), "text/plain", meta)

		_, gout, _ := h.get(t, bucket, "meta.txt")
		for k, want := range meta {
			if gout.Metadata[k] != want {
				failf(t, "GetObject metadata[%q]=%q, want %q (all=%v)", k, gout.Metadata[k], want, gout.Metadata)
			}
		}

		ex, hout := h.head(t, bucket, "meta.txt")
		for k, want := range meta {
			if hout.Metadata[k] != want {
				failf(t, "HeadObject metadata[%q]=%q, want %q", k, hout.Metadata[k], want)
			}
			if got := ex.Header.Get(metaHeader(k)); got != want {
				failf(t, "wire %s=%q, want %q", metaHeader(k), got, want)
			}
		}
	})

	t.Run("overwrite replaces metadata", func(t *testing.T) {
		colorTestResult(t)
		h.putMeta(t, bucket, "swap.txt", []byte("1"), "text/plain", map[string]string{"v": "1"})
		h.putMeta(t, bucket, "swap.txt", []byte("2"), "text/plain", map[string]string{"v": "2", "extra": "yes"})

		_, out := h.head(t, bucket, "swap.txt")
		if out.Metadata["v"] != "2" || out.Metadata["extra"] != "yes" {
			failf(t, "metadata after overwrite: %v", out.Metadata)
		}
		if _, ok := out.Metadata["missing"]; ok {
			fail(t, "unexpected leftover metadata key")
		}
	})

	t.Run("empty metadata map omits headers", func(t *testing.T) {
		colorTestResult(t)
		h.put(t, bucket, "plain.txt", []byte("x"), "text/plain")
		_, out := h.head(t, bucket, "plain.txt")
		if len(out.Metadata) != 0 {
			failf(t, "expected no metadata, got %v", out.Metadata)
		}
	})

	t.Run("metadata survives copy REPLACE directive with new values", func(t *testing.T) {
		colorTestResult(t)
		h.putMeta(t, bucket, "from", []byte("x"), "text/plain", map[string]string{"a": "1"})
		_, err := h.client.CopyObject(bg, &s3.CopyObjectInput{
			Bucket:            aws.String(bucket),
			Key:               aws.String("to"),
			CopySource:        aws.String(bucket + "/from"),
			MetadataDirective: types.MetadataDirectiveReplace,
			Metadata:          map[string]string{"a": "2", "b": "new"},
			ContentType:       aws.String("text/plain"),
		})
		if err != nil {
			failf(t, "CopyObject REPLACE: %v", err)
		}
		_, out := h.head(t, bucket, "to")
		if out.Metadata["a"] != "2" || out.Metadata["b"] != "new" {
			failf(t, "REPLACE metadata = %v", out.Metadata)
		}
	})
}
