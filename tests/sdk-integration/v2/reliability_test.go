package v2

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// =====================================================================
// Reliability — atomic overwrite / no torn reads
//
// PutObject must finalize via temp + rename (or equivalent). Concurrent
// readers must observe either the previous complete object or the new
// complete object — never a partial mix.
// =====================================================================

func TestAtomicOverwrite(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	key := "atomic.bin"

	const size = 1 << 20 // 1 MiB
	oldBody := bytes.Repeat([]byte("A"), size)
	newBody := bytes.Repeat([]byte("B"), size)
	h.put(t, bucket, key, oldBody, "application/octet-stream")

	var (
		wg      sync.WaitGroup
		bad     atomic.Int64
		reads   atomic.Int64
		olds    atomic.Int64
		news    atomic.Int64
		putDone = make(chan struct{})
	)

	const readers = 8
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				out, err := h.client.GetObject(bg, &s3.GetObjectInput{
					Bucket: aws.String(bucket),
					Key:    aws.String(key),
				})
				if err != nil {
					bad.Add(1)
					return
				}
				got, err := io.ReadAll(out.Body)
				out.Body.Close()
				if err != nil {
					bad.Add(1)
					return
				}
				reads.Add(1)
				switch {
				case bytes.Equal(got, oldBody):
					olds.Add(1)
				case bytes.Equal(got, newBody):
					news.Add(1)
				default:
					bad.Add(1)
					return
				}
				select {
				case <-putDone:
					return
				default:
				}
			}
		}()
	}

	_, err := h.client.PutObject(bg, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(newBody),
		ContentType: aws.String("application/octet-stream"),
	})
	close(putDone)
	wg.Wait()
	if err != nil {
		failf(t, "overwrite PutObject: %v", err)
	}

	if bad.Load() != 0 {
		failf(t, "observed torn/partial object (%d bad reads out of %d)", bad.Load(), reads.Load())
	}
	if reads.Load() == 0 {
		fail(t, "no concurrent reads completed")
	}
	_, _, final := h.get(t, bucket, key)
	if !bytes.Equal(final, newBody) {
		fail(t, "final object is not the overwritten content")
	}
	t.Logf("reads=%d old=%d new=%d", reads.Load(), olds.Load(), news.Load())
}

func TestPutToMissingBucketDoesNotSucceed(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	missing := uniqueBucket()

	ex, err := h.failing(t, func() error {
		_, err := h.client.PutObject(bg, &s3.PutObjectInput{
			Bucket: aws.String(missing),
			Key:    aws.String("orphan.txt"),
			Body:   bytes.NewReader([]byte("nope")),
		})
		return err
	})
	assertAPIError(t, ex, 404, "NoSuchBucket", map[string]string{"BucketName": missing})
	requireSDKError(t, err, ex, "NoSuchBucket")
}

func TestInterruptedUploadNotReadable(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	key := "interrupted.bin"

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		_, err := h.client.PutObject(bg, &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   pr,
		})
		errCh <- err
	}()

	if _, err := pw.Write(randBytes(64 << 10)); err != nil {
		failf(t, "write: %v", err)
	}
	// Abort the upload from the client side.
	_ = pw.CloseWithError(io.ErrUnexpectedEOF)
	uploadErr := <-errCh
	if uploadErr == nil {
		fail(t, "expected PutObject to fail after pipe abort")
	}

	_, err := h.client.HeadObject(bg, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		fail(t, "interrupted upload left a visible object")
	}
}
