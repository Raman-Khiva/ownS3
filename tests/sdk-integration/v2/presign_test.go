package v2

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// =====================================================================
// Presigned URLs — unsigned clients can GET/PUT with a valid signature
// embedded in the query string. Tampered URLs must fail.
// =====================================================================

func TestPresignedURLs(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("presigned GET", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		body := []byte("presigned-get-body")
		etag := h.put(t, bucket, "pub/get.txt", body, "text/plain")

		presigned, err := h.presign.PresignGetObject(bg, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("pub/get.txt"),
		}, s3.WithPresignExpires(5*time.Minute))
		if err != nil {
			failf(t, "PresignGetObject: %v", err)
		}

		status, hdr, got := unsignedGET(t, presigned.URL, nil)
		if status != http.StatusOK {
			failf(t, "presigned GET status %d, want 200; body=%s", status, got)
		}
		if !bytes.Equal(got, body) {
			fail(t, "presigned GET body mismatch")
		}
		if hdr.Get("ETag") != etag {
			failf(t, "ETag %q, want %q", hdr.Get("ETag"), etag)
		}
	})

	t.Run("presigned PUT then GET", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		key := "pub/upload.bin"
		body := randBytes(8 << 10)

		presigned, err := h.presign.PresignPutObject(bg, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			ContentType: aws.String("application/octet-stream"),
		}, s3.WithPresignExpires(5*time.Minute))
		if err != nil {
			failf(t, "PresignPutObject: %v", err)
		}

		status, _, respBody := unsignedPUT(t, presigned.URL, body, "application/octet-stream")
		if status != http.StatusOK {
			failf(t, "presigned PUT status %d, body=%s", status, respBody)
		}

		_, _, got := h.get(t, bucket, key)
		if !bytes.Equal(got, body) {
			fail(t, "object uploaded via presigned PUT does not match")
		}
	})

	t.Run("tampered presigned GET is AccessDenied", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		h.put(t, bucket, "pub/secure.txt", []byte("secret"), "text/plain")

		presigned, err := h.presign.PresignGetObject(bg, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("pub/secure.txt"),
		}, s3.WithPresignExpires(5*time.Minute))
		if err != nil {
			failf(t, "PresignGetObject: %v", err)
		}

		tampered := presigned.URL
		switch {
		case strings.Contains(tampered, "X-Amz-Signature="):
			tampered = strings.Replace(tampered, "X-Amz-Signature=", "X-Amz-Signature=deadbeef", 1)
		default:
			fail(t, "presigned URL missing X-Amz-Signature")
		}

		status, hdr, body := unsignedGET(t, tampered, nil)
		ex := exchange{
			Method: "GET",
			Path:   "/" + bucket + "/pub/secure.txt",
			Status: status,
			Header: hdr,
			Body:   body,
		}
		assertAPIError(t, ex, http.StatusForbidden, "AccessDenied", nil)
	})

	t.Run("presigned GET for missing key is NoSuchKey", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		presigned, err := h.presign.PresignGetObject(bg, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("missing.txt"),
		}, s3.WithPresignExpires(5*time.Minute))
		if err != nil {
			failf(t, "PresignGetObject: %v", err)
		}
		status, hdr, body := unsignedGET(t, presigned.URL, nil)
		ex := exchange{
			Method: "GET",
			Path:   "/" + bucket + "/missing.txt",
			Status: status,
			Header: hdr,
			Body:   body,
		}
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchKey", map[string]string{"Key": "missing.txt"})
	})
}
