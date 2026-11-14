package v2

import (
	"bytes"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// =====================================================================
// Stock SDK checksum behavior
//
// v1 forced RequestChecksumCalculationWhenRequired so aws-chunked /
// flexible checksums never appeared. Real apps use SDK defaults
// (WhenSupported). V2 requires Put/Get/multipart to succeed under that mode.
// =====================================================================

func TestSDKChecksumDefaults(t *testing.T) {
	colorTestResult(t)
	h := newHarnessWith(t, harnessOpts{
		checksums: aws.RequestChecksumCalculationWhenSupported,
	})

	t.Run("put get round-trip with default checksums", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		body := randBytes(128 << 10)

		_, err := h.client.PutObject(bg, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String("checksum.bin"),
			Body:        bytes.NewReader(body),
			ContentType: aws.String("application/octet-stream"),
		})
		if err != nil {
			failf(t, "PutObject with SDK checksums: %v", err)
		}

		out, err := h.client.GetObject(bg, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("checksum.bin"),
		})
		if err != nil {
			failf(t, "GetObject: %v", err)
		}
		defer out.Body.Close()
		got, err := io.ReadAll(out.Body)
		if err != nil {
			failf(t, "read: %v", err)
		}
		if !bytes.Equal(got, body) {
			fail(t, "checksum-mode round-trip corrupted data")
		}
	})

	t.Run("multipart with default checksums", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		key := "checksum-mp.bin"
		part := randBytes(partSize)

		created, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			failf(t, "CreateMultipartUpload: %v", err)
		}
		partOut, err := h.client.UploadPart(bg, &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   created.UploadId,
			PartNumber: aws.Int32(1),
			Body:       bytes.NewReader(part),
		})
		if err != nil {
			failf(t, "UploadPart with checksums: %v", err)
		}
		_, err = h.client.CompleteMultipartUpload(bg, &s3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: created.UploadId,
			MultipartUpload: &types.CompletedMultipartUpload{
				Parts: []types.CompletedPart{{
					ETag:       partOut.ETag,
					PartNumber: aws.Int32(1),
				}},
			},
		})
		if err != nil {
			failf(t, "CompleteMultipartUpload: %v", err)
		}
		_, _, got := h.get(t, bucket, key)
		if !bytes.Equal(got, part) {
			fail(t, "multipart checksum-mode body mismatch")
		}
	})
}
