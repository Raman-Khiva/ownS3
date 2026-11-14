package v2

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// =====================================================================
// Range GET — 206 Partial Content
// =====================================================================

func TestGetObjectRange(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	body := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	etag := h.put(t, bucket, "range.txt", body, "text/plain")

	t.Run("bytes=0-4", func(t *testing.T) {
		colorTestResult(t)
		h.rec.reset()
		out, err := h.client.GetObject(bg, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("range.txt"),
			Range:  aws.String("bytes=0-4"),
		})
		if err != nil {
			failf(t, "GetObject Range: %v", err)
		}
		defer out.Body.Close()
		got, err := io.ReadAll(out.Body)
		if err != nil {
			failf(t, "read body: %v", err)
		}
		ex := h.rec.one(t)

		assertResponse(t, ex, http.StatusPartialContent, kindRaw)
		if !bytes.Equal(got, body[0:5]) {
			failf(t, "body %q, want %q", got, body[0:5])
		}
		wantCR := fmt.Sprintf("bytes 0-4/%d", len(body))
		if ex.Header.Get("Content-Range") != wantCR {
			failf(t, "Content-Range %q, want %q", ex.Header.Get("Content-Range"), wantCR)
		}
		if ex.Header.Get("Content-Length") != "5" {
			failf(t, "Content-Length %q, want 5", ex.Header.Get("Content-Length"))
		}
		if ex.Header.Get("ETag") != etag {
			failf(t, "ETag %q, want %q", ex.Header.Get("ETag"), etag)
		}
		if aws.ToString(out.ContentRange) != wantCR {
			failf(t, "SDK ContentRange %q", aws.ToString(out.ContentRange))
		}
	})

	t.Run("bytes=10- (suffix from offset)", func(t *testing.T) {
		colorTestResult(t)
		out, err := h.client.GetObject(bg, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("range.txt"),
			Range:  aws.String("bytes=10-"),
		})
		if err != nil {
			failf(t, "GetObject Range open end: %v", err)
		}
		defer out.Body.Close()
		got, err := io.ReadAll(out.Body)
		if err != nil {
			failf(t, "read: %v", err)
		}
		if !bytes.Equal(got, body[10:]) {
			failf(t, "body %q, want %q", got, body[10:])
		}
		wantCR := fmt.Sprintf("bytes 10-%d/%d", len(body)-1, len(body))
		if aws.ToString(out.ContentRange) != wantCR {
			failf(t, "ContentRange %q, want %q", aws.ToString(out.ContentRange), wantCR)
		}
	})

	t.Run("bytes=-5 suffix", func(t *testing.T) {
		colorTestResult(t)
		out, err := h.client.GetObject(bg, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("range.txt"),
			Range:  aws.String("bytes=-5"),
		})
		if err != nil {
			failf(t, "GetObject suffix Range: %v", err)
		}
		defer out.Body.Close()
		got, err := io.ReadAll(out.Body)
		if err != nil {
			failf(t, "read: %v", err)
		}
		want := body[len(body)-5:]
		if !bytes.Equal(got, want) {
			failf(t, "body %q, want %q", got, want)
		}
	})

	t.Run("full object still 200 without Range", func(t *testing.T) {
		colorTestResult(t)
		ex, _, got := h.get(t, bucket, "range.txt")
		assertResponse(t, ex, http.StatusOK, kindRaw)
		if ex.Header.Get("Content-Range") != "" {
			fail(t, "Content-Range must be absent on full GET")
		}
		if !bytes.Equal(got, body) {
			fail(t, "full GET body mismatch")
		}
		if ex.Header.Get("Accept-Ranges") != "bytes" {
			failf(t, "Accept-Ranges %q", ex.Header.Get("Accept-Ranges"))
		}
	})

	t.Run("unsatisfiable range", func(t *testing.T) {
		colorTestResult(t)
		ex, err := h.failing(t, func() error {
			out, err := h.client.GetObject(bg, &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String("range.txt"),
				Range:  aws.String("bytes=1000-2000"),
			})
			if out != nil && out.Body != nil {
				out.Body.Close()
			}
			return err
		})
		assertAPIError(t, ex, http.StatusRequestedRangeNotSatisfiable, "InvalidRange", nil)
		requireSDKError(t, err, ex, "InvalidRange")
	})

	t.Run("HeadObject ignores Range body but reports size", func(t *testing.T) {
		colorTestResult(t)
		ex, out := h.head(t, bucket, "range.txt")
		assertResponse(t, ex, http.StatusOK, kindEmpty)
		if aws.ToInt64(out.ContentLength) != int64(len(body)) {
			failf(t, "ContentLength %d", aws.ToInt64(out.ContentLength))
		}
		if cl := ex.Header.Get("Content-Length"); cl != strconv.Itoa(len(body)) {
			failf(t, "HEAD Content-Length %q", cl)
		}
	})
}
