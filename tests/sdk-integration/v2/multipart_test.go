package v2

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// =====================================================================
// Multipart upload — Create / UploadPart / Complete / Abort / ListParts
//
// Part size: MinIO-compatible (no 5 MiB AWS minimum). Tests use ~256 KiB
// parts so the suite stays fast for local development.
// =====================================================================

const partSize = 256 << 10 // 256 KiB

func TestMultipartUpload(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("happy path three parts", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		key := "big/multipart.bin"
		parts := [][]byte{
			randBytes(partSize),
			randBytes(partSize),
			randBytes(partSize / 2), // last part may be smaller
		}
		var whole []byte
		for _, p := range parts {
			whole = append(whole, p...)
		}

		h.rec.reset()
		created, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			ContentType: aws.String("application/octet-stream"),
			Metadata:    map[string]string{"source": "multipart"},
		})
		if err != nil {
			failf(t, "CreateMultipartUpload: %v", err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindXML)
		uploadID := aws.ToString(created.UploadId)
		if uploadID == "" {
			fail(t, "UploadId empty")
		}

		var completed []types.CompletedPart
		for i, body := range parts {
			pn := int32(i + 1)
			h.rec.reset()
			partOut, err := h.client.UploadPart(bg, &s3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(uploadID),
				PartNumber: aws.Int32(pn),
				Body:       bytes.NewReader(body),
			})
			if err != nil {
				failf(t, "UploadPart %d: %v", pn, err)
			}
			pex := h.rec.one(t)
			assertResponse(t, pex, http.StatusOK, kindEmpty)
			etag := aws.ToString(partOut.ETag)
			if etag == "" {
				failf(t, "part %d missing ETag", pn)
			}
			completed = append(completed, types.CompletedPart{
				ETag:       aws.String(etag),
				PartNumber: aws.Int32(pn),
			})
		}

		// ListParts sees uploaded parts.
		listed, err := h.client.ListParts(bg, &s3.ListPartsInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		if err != nil {
			failf(t, "ListParts: %v", err)
		}
		if len(listed.Parts) != 3 {
			failf(t, "ListParts count %d, want 3", len(listed.Parts))
		}

		h.rec.reset()
		comp, err := h.client.CompleteMultipartUpload(bg, &s3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
			MultipartUpload: &types.CompletedMultipartUpload{
				Parts: completed,
			},
		})
		if err != nil {
			failf(t, "CompleteMultipartUpload: %v", err)
		}
		cex := h.rec.one(t)
		assertResponse(t, cex, http.StatusOK, kindXML)
		finalETag := aws.ToString(comp.ETag)
		if !multipartETagRe.MatchString(finalETag) {
			failf(t, "multipart ETag %q must match \"{md5}-{nparts}\"", finalETag)
		}

		gx, gout, got := h.get(t, bucket, key)
		if !bytes.Equal(got, whole) {
			fail(t, "multipart assembled body mismatch")
		}
		if gx.Header.Get("Content-Type") != "application/octet-stream" {
			failf(t, "Content-Type %q", gx.Header.Get("Content-Type"))
		}
		if gout.Metadata["source"] != "multipart" {
			failf(t, "metadata lost: %v", gout.Metadata)
		}
		if aws.ToString(gout.ETag) != finalETag {
			failf(t, "GetObject ETag %q != complete ETag %q", aws.ToString(gout.ETag), finalETag)
		}

		// Upload no longer listable after complete.
		_, aerr := h.failing(t, func() error {
			_, err := h.client.ListParts(bg, &s3.ListPartsInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				UploadId: aws.String(uploadID),
			})
			return err
		})
		_ = aerr
	})

	t.Run("abort removes in-progress upload", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		key := "abort-me.bin"
		created, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			failf(t, "CreateMultipartUpload: %v", err)
		}
		uploadID := created.UploadId
		_, err = h.client.UploadPart(bg, &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   uploadID,
			PartNumber: aws.Int32(1),
			Body:       bytes.NewReader(randBytes(partSize)),
		})
		if err != nil {
			failf(t, "UploadPart: %v", err)
		}

		ex := h.ok(t, func() error {
			_, err := h.client.AbortMultipartUpload(bg, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				UploadId: uploadID,
			})
			return err
		})
		assertResponse(t, ex, http.StatusNoContent, kindEmpty)

		// Object must not exist.
		_, gerr := h.failing(t, func() error {
			_, err := h.client.GetObject(bg, &s3.GetObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(key),
			})
			return err
		})
		_ = gerr

		// Further UploadPart fails.
		ex2, _ := h.failing(t, func() error {
			_, err := h.client.UploadPart(bg, &s3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   uploadID,
				PartNumber: aws.Int32(2),
				Body:       bytes.NewReader(randBytes(32)),
			})
			return err
		})
		if ex2.Status != http.StatusNotFound {
			failf(t, "UploadPart after abort status %d, want 404", ex2.Status)
		}
	})

	t.Run("overwrite via multipart replaces object", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		key := "replace.bin"
		h.put(t, bucket, key, []byte("old-single-put"), "text/plain")

		created, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			ContentType: aws.String("application/octet-stream"),
		})
		if err != nil {
			failf(t, "CreateMultipartUpload: %v", err)
		}
		part := randBytes(partSize)
		partOut, err := h.client.UploadPart(bg, &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   created.UploadId,
			PartNumber: aws.Int32(1),
			Body:       bytes.NewReader(part),
		})
		if err != nil {
			failf(t, "UploadPart: %v", err)
		}
		_, err = h.client.CompleteMultipartUpload(bg, &s3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: created.UploadId,
			MultipartUpload: &types.CompletedMultipartUpload{
				Parts: []types.CompletedPart{{
					ETag: partOut.ETag, PartNumber: aws.Int32(1),
				}},
			},
		})
		if err != nil {
			failf(t, "CompleteMultipartUpload: %v", err)
		}
		_, _, got := h.get(t, bucket, key)
		if !bytes.Equal(got, part) {
			fail(t, "multipart overwrite did not replace content")
		}
	})

	t.Run("unknown upload id", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.UploadPart(bg, &s3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String("k"),
				UploadId:   aws.String("00000000-0000-0000-0000-000000000000"),
				PartNumber: aws.Int32(1),
				Body:       bytes.NewReader([]byte("x")),
			})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchUpload", nil)
		requireSDKError(t, err, ex, "NoSuchUpload")
	})

	t.Run("complete with wrong part etag fails", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		key := "bad-etag.bin"
		created, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			failf(t, "CreateMultipartUpload: %v", err)
		}
		_, err = h.client.UploadPart(bg, &s3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   created.UploadId,
			PartNumber: aws.Int32(1),
			Body:       bytes.NewReader(randBytes(partSize)),
		})
		if err != nil {
			failf(t, "UploadPart: %v", err)
		}
		ex, _ := h.failing(t, func() error {
			_, err := h.client.CompleteMultipartUpload(bg, &s3.CompleteMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				UploadId: created.UploadId,
				MultipartUpload: &types.CompletedMultipartUpload{
					Parts: []types.CompletedPart{{
						ETag:       aws.String(`"00000000000000000000000000000000"`),
						PartNumber: aws.Int32(1),
					}},
				},
			})
			return err
		})
		if ex.Status != http.StatusBadRequest {
			failf(t, "status %d, want 400", ex.Status)
		}
	})
}

func TestListMultipartUploads(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)

	u1, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String("up/one"),
	})
	if err != nil {
		failf(t, "create 1: %v", err)
	}
	u2, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String("up/two"),
	})
	if err != nil {
		failf(t, "create 2: %v", err)
	}
	t.Cleanup(func() {
		h.client.AbortMultipartUpload(bg, &s3.AbortMultipartUploadInput{
			Bucket: aws.String(bucket), Key: aws.String("up/one"), UploadId: u1.UploadId,
		})
		h.client.AbortMultipartUpload(bg, &s3.AbortMultipartUploadInput{
			Bucket: aws.String(bucket), Key: aws.String("up/two"), UploadId: u2.UploadId,
		})
	})

	out, err := h.client.ListMultipartUploads(bg, &s3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		failf(t, "ListMultipartUploads: %v", err)
	}
	ids := map[string]bool{}
	for _, u := range out.Uploads {
		ids[aws.ToString(u.UploadId)] = true
	}
	if !ids[aws.ToString(u1.UploadId)] || !ids[aws.ToString(u2.UploadId)] {
		failf(t, "uploads %v missing created ids", ids)
	}
}
