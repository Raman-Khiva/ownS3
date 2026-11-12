package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// --------------------------------------------------
// HTTP RESPONSE RECORDER
// --------------------------------------------------

type responseRecorder struct {
	base http.RoundTripper

	mu     sync.Mutex
	status int
	header http.Header
	body   []byte
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{
		base: http.DefaultTransport,
	}
}

func (t *responseRecorder) RoundTrip(
	req *http.Request,
) (*http.Response, error) {

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	var rawBody []byte

	// Capture XML responses while preserving the body
	// for the AWS SDK deserializer.
	contentType, _, _ := mime.ParseMediaType(
		resp.Header.Get("Content-Type"),
	)

	if strings.Contains(contentType, "xml") && resp.Body != nil {
		rawBody, err = io.ReadAll(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, err
		}

		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(rawBody))
	}

	t.mu.Lock()
	t.status = resp.StatusCode
	t.header = resp.Header.Clone()
	t.body = rawBody
	t.mu.Unlock()

	return resp, nil
}

func (t *responseRecorder) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.status = 0
	t.header = nil
	t.body = nil
}

func (t *responseRecorder) snapshot() (
	int,
	http.Header,
	[]byte,
) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.status, t.header.Clone(), append([]byte(nil), t.body...)
}

// --------------------------------------------------
// ASSERTION HELPERS
// --------------------------------------------------

func check(operation string, err error) {
	if err != nil {
		log.Fatalf("[FAIL] %s: %v", operation, err)
	}
}

func validateResponse(
	operation string,
	err error,
	recorder *responseRecorder,
	expectedStatus int,
) http.Header {

	check(operation, err)

	status, headers, _ := recorder.snapshot()

	if status != expectedStatus {
		log.Fatalf(
			"[FAIL] %s: expected HTTP %d, received HTTP %d",
			operation,
			expectedStatus,
			status,
		)
	}

	if headers.Get("X-Amz-Request-Id") == "" {
		log.Fatalf(
			"[FAIL] %s: missing X-Amz-Request-Id",
			operation,
		)
	}

	fmt.Printf(
		"[PASS] %s: HTTP %d\n",
		operation,
		status,
	)

	return headers
}

func requireString(name, actual, expected string) {
	if actual != expected {
		log.Fatalf(
			"[FAIL] %s: expected %q, received %q",
			name,
			expected,
			actual,
		)
	}
}

func requireNonEmpty(name, value string) {
	if value == "" {
		log.Fatalf("[FAIL] %s is missing", name)
	}
}

func requireInt64(name string, actual, expected int64) {
	if actual != expected {
		log.Fatalf(
			"[FAIL] %s: expected %d, received %d",
			name,
			expected,
			actual,
		)
	}
}

func requireHeader(
	headers http.Header,
	name string,
	expected string,
) {
	actual := headers.Get(name)

	requireString("Header: "+name, actual, expected)
}

func requireTime(name string, value *time.Time) {
	if value == nil || value.IsZero() {
		log.Fatalf("[FAIL] %s is missing or invalid", name)
	}
}

// --------------------------------------------------
// LIST OBJECTS XML VALIDATION
// --------------------------------------------------

const s3XMLNamespace = "http://s3.amazonaws.com/doc/2006-03-01/"

type listObjectsXML struct {
	XMLName xml.Name `xml:"ListBucketResult"`

	Name                  *string     `xml:"Name"`
	Prefix                *string     `xml:"Prefix"`
	MaxKeys               *int32      `xml:"MaxKeys"`
	KeyCount              *int32      `xml:"KeyCount"`
	IsTruncated           *bool       `xml:"IsTruncated"`
	EncodingType          *string     `xml:"EncodingType"`
	ContinuationToken     *string     `xml:"ContinuationToken"`
	NextContinuationToken *string     `xml:"NextContinuationToken"`
	Contents              []objectXML `xml:"Contents"`
}

type objectXML struct {
	Key          *string `xml:"Key"`
	LastModified *string `xml:"LastModified"`
	ETag         *string `xml:"ETag"`
	Size         *int64  `xml:"Size"`
	StorageClass *string `xml:"StorageClass"`
}

func validateListXML(
	raw []byte,
	bucket string,
	key string,
	content string,
	expectedETag string,
) {
	var result listObjectsXML

	if err := xml.Unmarshal(raw, &result); err != nil {
		log.Fatalf("[FAIL] Invalid ListObjectsV2 XML: %v", err)
	}

	if result.XMLName.Space != s3XMLNamespace {
		log.Fatalf(
			"[FAIL] Incorrect XML namespace: %q",
			result.XMLName.Space,
		)
	}

	if result.Name == nil ||
		result.Prefix == nil ||
		result.MaxKeys == nil ||
		result.KeyCount == nil ||
		result.IsTruncated == nil {
		log.Fatal("[FAIL] Missing required ListBucketResult fields")
	}

	requireString("XML Name", *result.Name, bucket)
	requireString("XML Prefix", *result.Prefix, "")
	requireInt64("XML MaxKeys", int64(*result.MaxKeys), 1000)
	requireInt64("XML KeyCount", int64(*result.KeyCount), 1)

	if *result.IsTruncated {
		log.Fatal("[FAIL] Unexpected IsTruncated=true")
	}

	// These fields should not appear when they were not requested
	// or when no continuation token is necessary.
	if result.EncodingType != nil {
		log.Fatal("[FAIL] Unexpected EncodingType")
	}

	if result.ContinuationToken != nil {
		log.Fatal("[FAIL] Unexpected ContinuationToken")
	}

	if result.NextContinuationToken != nil {
		log.Fatal("[FAIL] Unexpected NextContinuationToken")
	}

	if len(result.Contents) != 1 {
		log.Fatalf(
			"[FAIL] Expected 1 Contents element, received %d",
			len(result.Contents),
		)
	}

	object := result.Contents[0]

	if object.Key == nil ||
		object.LastModified == nil ||
		object.ETag == nil ||
		object.Size == nil ||
		object.StorageClass == nil {
		log.Fatal("[FAIL] Missing required Contents fields")
	}

	requireString("XML Object Key", *object.Key, key)
	requireString("XML Object ETag", *object.ETag, expectedETag)
	requireInt64("XML Object Size", *object.Size, int64(len(content)))
	requireString("XML StorageClass", *object.StorageClass, "STANDARD")

	if _, err := time.Parse(time.RFC3339Nano, *object.LastModified); err != nil {
		log.Fatalf("[FAIL] Invalid LastModified: %v", err)
	}

	fmt.Println("[PASS] ListObjectsV2 XML structure and fields")
}

// --------------------------------------------------
// MAIN
// --------------------------------------------------

func main() {
	ctx := context.Background()

	recorder := newResponseRecorder()

	httpClient := &http.Client{
		Transport: recorder,
	}

	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion("us-east-1"),
		config.WithHTTPClient(httpClient),
		config.WithRetryer(func() aws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.MaxAttempts = 1
			})
		}),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				"test-access-key",
				"test-secret-key",
				"",
			),
		),
	)
	check("LoadConfig", err)

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://localhost:9000")
		o.UsePathStyle = true
	})

	bucket := "sdk-test-bucket-31"
	key := "hello.txt"
	content := "Hello from AWS SDK to ownS3!"
	contentType := "text/plain"

	expectedSize := int64(len(content))

	var expectedETag string

	// --------------------------------------------------
	// 1. CREATE BUCKET
	// --------------------------------------------------

	recorder.reset()

	createResult, err := client.CreateBucket(
		ctx,
		&s3.CreateBucketInput{
			Bucket: aws.String(bucket),
		},
	)

	validateResponse(
		"CreateBucket",
		err,
		recorder,
		http.StatusOK,
	)

	if createResult == nil {
		log.Fatal("[FAIL] CreateBucket returned nil response")
	}

	fmt.Println("[PASS] CreateBucket response structure")

	// --------------------------------------------------
	// 2. PUT OBJECT
	// --------------------------------------------------

	recorder.reset()

	putResult, err := client.PutObject(
		ctx,
		&s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        strings.NewReader(content),
			ContentType: aws.String(contentType),
		},
	)

	putHeaders := validateResponse(
		"PutObject",
		err,
		recorder,
		http.StatusOK,
	)

	if putResult == nil {
		log.Fatal("[FAIL] PutObject returned nil response")
	}

	expectedETag = aws.ToString(putResult.ETag)

	requireNonEmpty("PutObject ETag", expectedETag)

	requireHeader(
		putHeaders,
		"ETag",
		expectedETag,
	)

	fmt.Println("[PASS] PutObject response fields")

	// --------------------------------------------------
	// 3. GET OBJECT
	// --------------------------------------------------

	recorder.reset()

	getResult, err := client.GetObject(
		ctx,
		&s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		},
	)

	getHeaders := validateResponse(
		"GetObject",
		err,
		recorder,
		http.StatusOK,
	)

	if getResult == nil {
		log.Fatal("[FAIL] GetObject returned nil response")
	}

	if getResult.Body == nil {
		log.Fatal("[FAIL] GetObject returned nil body")
	}

	defer getResult.Body.Close()

	requireInt64(
		"GetObject ContentLength",
		aws.ToInt64(getResult.ContentLength),
		expectedSize,
	)

	requireString(
		"GetObject ContentType",
		aws.ToString(getResult.ContentType),
		contentType,
	)

	requireString(
		"GetObject ETag",
		aws.ToString(getResult.ETag),
		expectedETag,
	)

	requireTime("GetObject LastModified", getResult.LastModified)

	requireHeader(
		getHeaders,
		"Content-Type",
		contentType,
	)

	requireHeader(
		getHeaders,
		"Content-Length",
		fmt.Sprint(expectedSize),
	)

	body, err := io.ReadAll(getResult.Body)
	check("ReadObjectBody", err)

	if !bytes.Equal(body, []byte(content)) {
		log.Fatal("[FAIL] Downloaded bytes differ from uploaded bytes")
	}

	fmt.Println("[PASS] GetObject metadata and body")

	// --------------------------------------------------
	// 4. HEAD OBJECT
	// --------------------------------------------------

	recorder.reset()

	headResult, err := client.HeadObject(
		ctx,
		&s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		},
	)

	headHeaders := validateResponse(
		"HeadObject",
		err,
		recorder,
		http.StatusOK,
	)

	if headResult == nil {
		log.Fatal("[FAIL] HeadObject returned nil response")
	}

	requireInt64(
		"HeadObject ContentLength",
		aws.ToInt64(headResult.ContentLength),
		expectedSize,
	)

	requireString(
		"HeadObject ContentType",
		aws.ToString(headResult.ContentType),
		contentType,
	)

	requireString(
		"HeadObject ETag",
		aws.ToString(headResult.ETag),
		expectedETag,
	)

	requireTime("HeadObject LastModified", headResult.LastModified)

	requireHeader(headHeaders, "Content-Type", contentType)
	requireHeader(
		headHeaders,
		"Content-Length",
		fmt.Sprint(expectedSize),
	)
	requireHeader(headHeaders, "ETag", expectedETag)

	fmt.Println("[PASS] HeadObject metadata")

	// --------------------------------------------------
	// 5. LIST OBJECTS V2
	// --------------------------------------------------

	recorder.reset()

	listing, err := client.ListObjectsV2(
		ctx,
		&s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
		},
	)

	listHeaders := validateResponse(
		"ListObjectsV2",
		err,
		recorder,
		http.StatusOK,
	)

	if listing == nil {
		log.Fatal("[FAIL] ListObjectsV2 returned nil response")
	}

	requireString(
		"ListObjectsV2 Name",
		aws.ToString(listing.Name),
		bucket,
	)

	requireString(
		"ListObjectsV2 Prefix",
		aws.ToString(listing.Prefix),
		"",
	)

	requireInt64(
		"ListObjectsV2 MaxKeys",
		int64(aws.ToInt32(listing.MaxKeys)),
		1000,
	)

	requireInt64(
		"ListObjectsV2 KeyCount",
		int64(aws.ToInt32(listing.KeyCount)),
		1,
	)

	if aws.ToBool(listing.IsTruncated) {
		log.Fatal("[FAIL] Unexpected truncated listing")
	}

	if len(listing.Contents) != 1 {
		log.Fatalf(
			"[FAIL] Expected 1 object, received %d",
			len(listing.Contents),
		)
	}

	object := listing.Contents[0]

	if object.Key == nil ||
		object.LastModified == nil ||
		object.ETag == nil ||
		object.Size == nil {
		log.Fatal("[FAIL] Missing required SDK object fields")
	}

	requireString("Listed Key", *object.Key, key)
	requireString("Listed ETag", *object.ETag, expectedETag)
	requireInt64("Listed Size", *object.Size, expectedSize)
	requireString("Listed StorageClass", string(object.StorageClass), "STANDARD")

	requireTime("Listed LastModified", object.LastModified)

	requireHeader(
		listHeaders,
		"Content-Type",
		"application/xml",
	)

	_, _, rawXML := recorder.snapshot()

	if len(rawXML) == 0 {
		log.Fatal("[FAIL] Missing raw ListObjectsV2 XML response")
	}

	validateListXML(
		rawXML,
		bucket,
		key,
		content,
		expectedETag,
	)

	fmt.Println("[PASS] ListObjectsV2 SDK fields")

	// --------------------------------------------------
	// 6. DELETE OBJECT
	// --------------------------------------------------

	recorder.reset()

	deleteObjectResult, err := client.DeleteObject(
		ctx,
		&s3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		},
	)

	validateResponse(
		"DeleteObject",
		err,
		recorder,
		http.StatusNoContent,
	)

	if deleteObjectResult == nil {
		log.Fatal("[FAIL] DeleteObject returned nil response")
	}

	// Verify actual deletion using HeadObject.
	recorder.reset()

	_, err = client.HeadObject(
		ctx,
		&s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		},
	)

	status, _, _ := recorder.snapshot()

	if err == nil || status != http.StatusNotFound {
		log.Fatalf(
			"[FAIL] Deleted object should return HTTP 404; status=%d err=%v",
			status,
			err,
		)
	}

	fmt.Println("[PASS] Deleted object is no longer accessible")

	// --------------------------------------------------
	// 7. DELETE BUCKET
	// --------------------------------------------------

	recorder.reset()

	deleteBucketResult, err := client.DeleteBucket(
		ctx,
		&s3.DeleteBucketInput{
			Bucket: aws.String(bucket),
		},
	)

	validateResponse(
		"DeleteBucket",
		err,
		recorder,
		http.StatusNoContent,
	)

	if deleteBucketResult == nil {
		log.Fatal("[FAIL] DeleteBucket returned nil response")
	}

	fmt.Println("\nALL SDK INTEGRATION VALIDATIONS PASSED")
}
