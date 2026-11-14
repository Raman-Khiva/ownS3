package v2

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// =====================================================================
// End-to-end MinIO-lite workflow through the SDK
// =====================================================================

func TestMinIOLiteLifecycle(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	src := uniqueBucket()
	dst := uniqueBucket()
	t.Cleanup(func() {
		h.purge(src)
		h.purge(dst)
	})

	// Create buckets
	for _, b := range []string{src, dst} {
		ex := h.ok(t, func() error {
			_, err := h.client.CreateBucket(bg, &s3.CreateBucketInput{Bucket: aws.String(b)})
			return err
		})
		assertResponse(t, ex, http.StatusOK, kindEmpty)
	}

	// Seed objects under a prefix
	bodies := map[string][]byte{
		"photos/a.jpg": []byte("aaa"),
		"photos/b.jpg": []byte("bbb"),
		"docs/readme":  []byte("readme"),
		"root.txt":     []byte("root"),
	}
	for k, v := range bodies {
		h.putMeta(t, src, k, v, "application/octet-stream", map[string]string{"k": k})
	}

	// Prefix listing
	listed, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
		Bucket: aws.String(src),
		Prefix: aws.String("photos/"),
	})
	if err != nil {
		failf(t, "list prefix: %v", err)
	}
	if len(listed.Contents) != 2 {
		failf(t, "photos/ count %d, want 2", len(listed.Contents))
	}

	// Delimiter listing
	delim, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
		Bucket:    aws.String(src),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		failf(t, "list delimiter: %v", err)
	}
	if len(delim.Contents) != 1 || aws.ToString(delim.Contents[0].Key) != "root.txt" {
		failf(t, "delimiter contents = %+v", delim.Contents)
	}
	if len(delim.CommonPrefixes) < 2 {
		failf(t, "expected docs/ and photos/ common prefixes, got %+v", delim.CommonPrefixes)
	}

	// Copy across buckets
	_, err = h.client.CopyObject(bg, &s3.CopyObjectInput{
		Bucket:     aws.String(dst),
		Key:        aws.String("photos/a.jpg"),
		CopySource: aws.String(src + "/photos/a.jpg"),
	})
	if err != nil {
		failf(t, "copy: %v", err)
	}
	_, _, got := h.get(t, dst, "photos/a.jpg")
	if !bytes.Equal(got, bodies["photos/a.jpg"]) {
		fail(t, "copied body mismatch")
	}

	// Multipart upload into dst
	mpBody := randBytes(partSize + 100)
	created, err := h.client.CreateMultipartUpload(bg, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(dst),
		Key:    aws.String("big.bin"),
	})
	if err != nil {
		failf(t, "CreateMultipartUpload: %v", err)
	}
	p1 := mpBody[:partSize]
	p2 := mpBody[partSize:]
	part1, err := h.client.UploadPart(bg, &s3.UploadPartInput{
		Bucket: aws.String(dst), Key: aws.String("big.bin"),
		UploadId: created.UploadId, PartNumber: aws.Int32(1),
		Body: bytes.NewReader(p1),
	})
	if err != nil {
		failf(t, "UploadPart 1: %v", err)
	}
	part2, err := h.client.UploadPart(bg, &s3.UploadPartInput{
		Bucket: aws.String(dst), Key: aws.String("big.bin"),
		UploadId: created.UploadId, PartNumber: aws.Int32(2),
		Body: bytes.NewReader(p2),
	})
	if err != nil {
		failf(t, "UploadPart 2: %v", err)
	}
	_, err = h.client.CompleteMultipartUpload(bg, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(dst), Key: aws.String("big.bin"), UploadId: created.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: []types.CompletedPart{
				{ETag: part1.ETag, PartNumber: aws.Int32(1)},
				{ETag: part2.ETag, PartNumber: aws.Int32(2)},
			},
		},
	})
	if err != nil {
		failf(t, "Complete: %v", err)
	}
	_, _, mpGot := h.get(t, dst, "big.bin")
	if !bytes.Equal(mpGot, mpBody) {
		fail(t, "multipart lifecycle body mismatch")
	}

	// Range get
	out, err := h.client.GetObject(bg, &s3.GetObjectInput{
		Bucket: aws.String(dst),
		Key:    aws.String("big.bin"),
		Range:  aws.String("bytes=0-99"),
	})
	if err != nil {
		failf(t, "Range GET: %v", err)
	}
	chunk, err := io.ReadAll(out.Body)
	out.Body.Close()
	if err != nil {
		failf(t, "read range: %v", err)
	}
	if !bytes.Equal(chunk, mpBody[:100]) {
		fail(t, "range chunk mismatch")
	}

	// Batch delete from src
	_, err = h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
		Bucket: aws.String(src),
		Delete: &types.Delete{
			Objects: []types.ObjectIdentifier{
				{Key: aws.String("photos/a.jpg")},
				{Key: aws.String("photos/b.jpg")},
				{Key: aws.String("docs/readme")},
				{Key: aws.String("root.txt")},
			},
			Quiet: aws.Bool(true),
		},
	})
	if err != nil {
		failf(t, "DeleteObjects: %v", err)
	}
	empty, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{Bucket: aws.String(src)})
	if err != nil {
		failf(t, "list after delete: %v", err)
	}
	if len(empty.Contents) != 0 {
		failf(t, "src should be empty, got %d keys", len(empty.Contents))
	}

	ex := h.ok(t, func() error {
		_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(src)})
		return err
	})
	assertResponse(t, ex, http.StatusNoContent, kindEmpty)
}
