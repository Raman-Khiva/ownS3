package v2

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// =====================================================================
// ListObjectsV2 — prefix, delimiter, pagination, start-after
// =====================================================================

func TestListObjectsV2Prefix(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)

	keys := []string{
		"a.txt",
		"photos/cat.jpg",
		"photos/dog.jpg",
		"photos/2024/trip.png",
		"docs/readme.md",
		"photosx/not-a-prefix-match.txt",
	}
	for _, k := range keys {
		h.put(t, bucket, k, []byte("body:"+k), "text/plain")
	}

	t.Run("prefix photos/", func(t *testing.T) {
		colorTestResult(t)
		h.rec.reset()
		out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String("photos/"),
		})
		if err != nil {
			failf(t, "ListObjectsV2: %v", err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindXML)

		var doc listObjectsXML
		decodeXML(t, ex.Body, &doc)
		requireNS(t, doc.XMLName)
		if doc.Prefix == nil || *doc.Prefix != "photos/" {
			failf(t, "XML Prefix = %v, want photos/", doc.Prefix)
		}
		want := []string{"photos/2024/trip.png", "photos/cat.jpg", "photos/dog.jpg"}
		if len(doc.Contents) != len(want) {
			failf(t, "Contents %d, want %d\n%s", len(doc.Contents), len(want), ex.Body)
		}
		for i, c := range doc.Contents {
			if aws.ToString(c.Key) != want[i] {
				failf(t, "Contents[%d].Key %q, want %q", i, aws.ToString(c.Key), want[i])
			}
		}
		if len(out.Contents) != len(want) {
			failf(t, "SDK Contents %d, want %d", len(out.Contents), len(want))
		}
		if aws.ToString(out.Prefix) != "photos/" {
			failf(t, "SDK Prefix %q", aws.ToString(out.Prefix))
		}
	})

	t.Run("prefix with no matches", func(t *testing.T) {
		colorTestResult(t)
		out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String("missing/"),
		})
		if err != nil {
			failf(t, "ListObjectsV2: %v", err)
		}
		if len(out.Contents) != 0 || aws.ToInt32(out.KeyCount) != 0 || aws.ToBool(out.IsTruncated) {
			failf(t, "expected empty result, got %+v", out.Contents)
		}
	})
}

func TestListObjectsV2Delimiter(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)

	for _, k := range []string{
		"root.txt",
		"photos/cat.jpg",
		"photos/dog.jpg",
		"photos/2024/a.png",
		"docs/a.md",
		"docs/b.md",
	} {
		h.put(t, bucket, k, []byte(k), "")
	}

	t.Run("delimiter slash at root", func(t *testing.T) {
		colorTestResult(t)
		h.rec.reset()
		out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
			Bucket:    aws.String(bucket),
			Delimiter: aws.String("/"),
		})
		if err != nil {
			failf(t, "ListObjectsV2: %v", err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindXML)

		var doc listObjectsXML
		decodeXML(t, ex.Body, &doc)
		requireNS(t, doc.XMLName)
		if doc.Delimiter == nil || *doc.Delimiter != "/" {
			failf(t, "Delimiter = %v, want /", doc.Delimiter)
		}
		if len(doc.Contents) != 1 || aws.ToString(doc.Contents[0].Key) != "root.txt" {
			failf(t, "Contents want [root.txt], got %+v", doc.Contents)
		}
		gotPrefixes := map[string]bool{}
		for _, cp := range doc.CommonPrefixes {
			noExtra(t, "CommonPrefixes", cp.Extra)
			if cp.Prefix == nil {
				fail(t, "CommonPrefixes entry missing Prefix")
			}
			gotPrefixes[*cp.Prefix] = true
		}
		for _, want := range []string{"docs/", "photos/"} {
			if !gotPrefixes[want] {
				failf(t, "missing CommonPrefix %q in %v", want, gotPrefixes)
			}
		}
		if len(gotPrefixes) != 2 {
			failf(t, "CommonPrefixes = %v, want docs/ and photos/", gotPrefixes)
		}
		if len(out.CommonPrefixes) != 2 || len(out.Contents) != 1 {
			failf(t, "SDK CommonPrefixes=%d Contents=%d", len(out.CommonPrefixes), len(out.Contents))
		}
	})

	t.Run("prefix + delimiter", func(t *testing.T) {
		colorTestResult(t)
		out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
			Bucket:    aws.String(bucket),
			Prefix:    aws.String("photos/"),
			Delimiter: aws.String("/"),
		})
		if err != nil {
			failf(t, "ListObjectsV2: %v", err)
		}
		keys := map[string]bool{}
		for _, o := range out.Contents {
			keys[aws.ToString(o.Key)] = true
		}
		if !keys["photos/cat.jpg"] || !keys["photos/dog.jpg"] || len(keys) != 2 {
			failf(t, "Contents keys = %v, want cat+dog under photos/", keys)
		}
		prefixes := map[string]bool{}
		for _, cp := range out.CommonPrefixes {
			prefixes[aws.ToString(cp.Prefix)] = true
		}
		if !prefixes["photos/2024/"] || len(prefixes) != 1 {
			failf(t, "CommonPrefixes = %v, want photos/2024/", prefixes)
		}
	})
}

func TestListObjectsV2Pagination(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)

	const n = 12
	for i := 0; i < n; i++ {
		key := "item-" + pad2(i) + ".txt"
		h.put(t, bucket, key, []byte(key), "")
	}

	t.Run("max-keys pages with continuation token", func(t *testing.T) {
		colorTestResult(t)
		seen := map[string]bool{}
		var token *string
		pages := 0
		for {
			h.rec.reset()
			out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
				Bucket:            aws.String(bucket),
				MaxKeys:           aws.Int32(5),
				ContinuationToken: token,
			})
			if err != nil {
				failf(t, "ListObjectsV2 page %d: %v", pages, err)
			}
			ex := h.rec.one(t)
			assertResponse(t, ex, http.StatusOK, kindXML)

			var doc listObjectsXML
			decodeXML(t, ex.Body, &doc)
			if doc.MaxKeys == nil || *doc.MaxKeys != 5 {
				failf(t, "MaxKeys = %v, want 5", doc.MaxKeys)
			}
			if len(out.Contents) == 0 || len(out.Contents) > 5 {
				failf(t, "page size %d, want 1..5", len(out.Contents))
			}
			for _, o := range out.Contents {
				k := aws.ToString(o.Key)
				if seen[k] {
					failf(t, "duplicate key across pages: %q", k)
				}
				seen[k] = true
			}
			pages++
			if !aws.ToBool(out.IsTruncated) {
				if out.NextContinuationToken != nil {
					fail(t, "NextContinuationToken set when not truncated")
				}
				break
			}
			if out.NextContinuationToken == nil || aws.ToString(out.NextContinuationToken) == "" {
				fail(t, "truncated page missing NextContinuationToken")
			}
			token = out.NextContinuationToken
			if pages > 10 {
				fail(t, "pagination did not terminate")
			}
		}
		if len(seen) != n {
			failf(t, "collected %d keys across pages, want %d", len(seen), n)
		}
		if pages < 3 {
			failf(t, "expected at least 3 pages with max-keys=5 over %d objects, got %d", n, pages)
		}
	})

	t.Run("max-keys capped at 1000", func(t *testing.T) {
		colorTestResult(t)
		h.rec.reset()
		out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
			Bucket:  aws.String(bucket),
			MaxKeys: aws.Int32(5000),
		})
		if err != nil {
			failf(t, "ListObjectsV2: %v", err)
		}
		ex := h.rec.one(t)
		var doc listObjectsXML
		decodeXML(t, ex.Body, &doc)
		if doc.MaxKeys == nil || *doc.MaxKeys != 1000 {
			failf(t, "MaxKeys must be capped at 1000, got %v", doc.MaxKeys)
		}
		if aws.ToInt32(out.MaxKeys) != 1000 {
			failf(t, "SDK MaxKeys %d, want 1000", aws.ToInt32(out.MaxKeys))
		}
	})

	t.Run("invalid continuation token", func(t *testing.T) {
		colorTestResult(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
				Bucket:            aws.String(bucket),
				ContinuationToken: aws.String("not-a-real-token"),
			})
			return err
		})
		assertAPIError(t, ex, http.StatusBadRequest, "InvalidArgument", nil)
		requireSDKError(t, err, ex, "InvalidArgument")
	})
}

func TestListObjectsV2StartAfter(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		h.put(t, bucket, k, []byte(k), "")
	}

	out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
		Bucket:     aws.String(bucket),
		StartAfter: aws.String("b"),
	})
	if err != nil {
		failf(t, "ListObjectsV2: %v", err)
	}
	var keys []string
	for _, o := range out.Contents {
		keys = append(keys, aws.ToString(o.Key))
	}
	want := []string{"c", "d", "e"}
	if len(keys) != len(want) {
		failf(t, "keys %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			failf(t, "keys %v, want %v", keys, want)
		}
	}
	if aws.ToString(out.StartAfter) != "b" {
		failf(t, "SDK StartAfter %q", aws.ToString(out.StartAfter))
	}
}

func TestListObjectsV1NotImplemented(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	h.put(t, bucket, "k", []byte("v"), "")

	ex, err := h.failing(t, func() error {
		_, err := h.client.ListObjects(bg, &s3.ListObjectsInput{Bucket: aws.String(bucket)})
		return err
	})
	if ex.Status != http.StatusNotImplemented {
		failf(t, "ListObjects v1 status %d, want 501", ex.Status)
	}
	assertAPIError(t, ex, http.StatusNotImplemented, "NotImplemented", nil)
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "NotImplemented" {
		failf(t, "SDK error code want NotImplemented, got %v", err)
	}
}

func pad2(i int) string {
	return fmt.Sprintf("%02d", i)
}