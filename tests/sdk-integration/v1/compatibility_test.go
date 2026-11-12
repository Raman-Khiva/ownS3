// AWS SDK compatibility suite, v1: the 9 "pure" routes (no query params, no
// conditional/range headers, no multipart, no versioning).
//
//	ListBuckets  CreateBucket  HeadBucket  DeleteBucket  ListObjectsV2
//	PutObject    GetObject     HeadObject  DeleteObject
//
// Run:   S3_ENDPOINT=http://localhost:9000 go test -v -count=1 ./...
//
// Needs a recent aws-sdk-go-v2 (RequestChecksumCalculation option, v1.73+).
// Checksums are set to "when required" so v1 does not depend on aws-chunked
// trailers; that belongs in v2 of the suite.
package v1

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Colored failure helpers. They preserve testing.T's failure behavior and
// report the original assertion call site; only the displayed message changes.
const (
	colorRedBold   = "\x1b[1;31m"
	colorGreenBold = "\x1b[1;32m"
	colorReset     = "\x1b[0m"
	failLabel      = colorRedBold + "✗ FAIL" + colorReset + " "
)

func failf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Fatalf(failLabel+format, args...)
}

func fail(t *testing.T, args ...any) {
	t.Helper()
	t.Fatal(failLabel + fmt.Sprint(args...))
}

// colorTestResult turns the terminal green when a test or subtest passes so
// the following go-test "--- PASS: ..." line is green too. Failed tests keep
// the red assertion output above. TestMain clears the color before exit.
func colorTestResult(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			fmt.Fprint(os.Stdout, colorReset)
			return
		}
		// No reset: leave green active for the testing package's PASS line.
		fmt.Fprintf(os.Stdout, "%s✓ PASS %s\n", colorGreenBold, t.Name())
	})
}

func TestMain(m *testing.M) {
	code := m.Run()
	fmt.Fprint(os.Stdout, colorReset)
	os.Exit(code)
}

var bg = context.Background()

const (
	s3NS    = "http://s3.amazonaws.com/doc/2006-03-01/"
	xmlDecl = `<?xml version="1.0" encoding="UTF-8"?>`
)

var (
	isoRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	etagRe = regexp.MustCompile(`^"[0-9a-f]{32}"$`)
)

// =====================================================================
// HTTP recorder: keeps every request/response so tests can assert on the
// exact wire format, not just what the SDK decoded.
// =====================================================================

type exchange struct {
	Method string
	Path   string
	Status int
	Header http.Header
	Body   []byte
}

type recorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	log  []exchange
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	var body []byte
	if resp.Body != nil {
		body, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}
	r.mu.Lock()
	r.log = append(r.log, exchange{
		Method: req.Method,
		Path:   req.URL.EscapedPath(),
		Status: resp.StatusCode,
		Header: resp.Header.Clone(),
		Body:   body,
	})
	r.mu.Unlock()
	return resp, nil
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.log = nil
	r.mu.Unlock()
}

// one fails the test unless exactly one HTTP request happened (catches
// retries, redirects and accidental extra calls).
func (r *recorder) one(t *testing.T) exchange {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.log) != 1 {
		var seen []string
		for _, e := range r.log {
			seen = append(seen, fmt.Sprintf("%s %s -> %d", e.Method, e.Path, e.Status))
		}
		failf(t, "expected exactly 1 HTTP request, got %d: %v", len(r.log), seen)
	}
	return r.log[0]
}

// =====================================================================
// Harness
// =====================================================================

type harness struct {
	client *s3.Client
	rec    *recorder
	base   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	endpoint := os.Getenv("S3_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:9000"
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableCompression = true
	rec := &recorder{base: tr}

	cfg, err := config.LoadDefaultConfig(bg,
		config.WithRegion("us-east-1"),
		config.WithHTTPClient(&http.Client{Transport: rec}),
		config.WithRetryer(func() aws.Retryer { return aws.NopRetryer{} }),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test-access-key", "test-secret-key", ""),
		),
	)
	if err != nil {
		failf(t, "LoadDefaultConfig: %v", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &harness{client: client, rec: rec, base: endpoint}
}

var (
	bucketSeq atomic.Int64
	runID     = strconv.FormatInt(time.Now().Unix(), 36)
)

func uniqueBucket() string {
	return fmt.Sprintf("sdkv1-%s-%d", runID, bucketSeq.Add(1))
}

// newBucket creates a bucket (setup, not asserted) and purges it afterwards.
func (h *harness) newBucket(t *testing.T) string {
	t.Helper()
	name := uniqueBucket()
	if _, err := h.client.CreateBucket(bg, &s3.CreateBucketInput{Bucket: aws.String(name)}); err != nil {
		failf(t, "setup CreateBucket(%s): %v", name, err)
	}
	t.Cleanup(func() { h.purge(name) })
	return name
}

func (h *harness) purge(bucket string) {
	if list, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}); err == nil {
		for _, o := range list.Contents {
			h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: o.Key})
		}
	}
	h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
}

// put is a setup helper (no wire assertions). Returns the ETag.
func (h *harness) put(t *testing.T, bucket, key string, body []byte, contentType string) string {
	t.Helper()
	in := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	out, err := h.client.PutObject(bg, in)
	if err != nil {
		failf(t, "setup PutObject(%s/%s): %v", bucket, key, err)
	}
	return aws.ToString(out.ETag)
}

// ok runs an operation that must succeed and returns the single exchange.
func (h *harness) ok(t *testing.T, op func() error) exchange {
	t.Helper()
	h.rec.reset()
	if err := op(); err != nil {
		failf(t, "operation failed: %v", err)
	}
	return h.rec.one(t)
}

// failing runs an operation that must fail and returns the exchange + error.
func (h *harness) failing(t *testing.T, op func() error) (exchange, error) {
	t.Helper()
	h.rec.reset()
	err := op()
	if err == nil {
		fail(t, "expected an error, got success")
	}
	return h.rec.one(t), err
}

func (h *harness) get(t *testing.T, bucket, key string) (exchange, *s3.GetObjectOutput, []byte) {
	t.Helper()
	h.rec.reset()
	out, err := h.client.GetObject(bg, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		failf(t, "GetObject(%s/%s): %v", bucket, key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		failf(t, "reading GetObject body: %v", err)
	}
	return h.rec.one(t), out, body
}

func (h *harness) head(t *testing.T, bucket, key string) (exchange, *s3.HeadObjectOutput) {
	t.Helper()
	h.rec.reset()
	out, err := h.client.HeadObject(bg, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		failf(t, "HeadObject(%s/%s): %v", bucket, key, err)
	}
	return h.rec.one(t), out
}

// =====================================================================
// XML models. Pointer fields detect presence; Extra catches any element
// the model does not know about (so unexpected elements fail the test).
// =====================================================================

type anyEl struct{ XMLName xml.Name }

type errorXML struct {
	XMLName      xml.Name `xml:"Error"`
	Code         *string  `xml:"Code"`
	Message      *string  `xml:"Message"`
	BucketName   *string  `xml:"BucketName"`
	Key          *string  `xml:"Key"`
	RequestID    *string  `xml:"RequestId"`
	HostID       *string  `xml:"HostId"`
	Resource     *string  `xml:"Resource"`
	Method       *string  `xml:"Method"`
	ResourceType *string  `xml:"ResourceType"`
	Extra        []anyEl  `xml:",any"`
}

type ownerXML struct {
	ID          *string `xml:"ID"`
	DisplayName *string `xml:"DisplayName"`
	Extra       []anyEl `xml:",any"`
}

type bucketXML struct {
	Name         *string `xml:"Name"`
	CreationDate *string `xml:"CreationDate"`
	Extra        []anyEl `xml:",any"`
}

type bucketsXML struct {
	Bucket []bucketXML `xml:"Bucket"`
	Extra  []anyEl     `xml:",any"`
}

type listBucketsXML struct {
	XMLName xml.Name    `xml:"ListAllMyBucketsResult"`
	Owner   *ownerXML   `xml:"Owner"`
	Buckets *bucketsXML `xml:"Buckets"`
	Extra   []anyEl     `xml:",any"`
}

type objectXML struct {
	Key          *string `xml:"Key"`
	LastModified *string `xml:"LastModified"`
	ETag         *string `xml:"ETag"`
	Size         *int64  `xml:"Size"`
	StorageClass *string `xml:"StorageClass"`
	Extra        []anyEl `xml:",any"`
}

type listObjectsXML struct {
	XMLName               xml.Name    `xml:"ListBucketResult"`
	Name                  *string     `xml:"Name"`
	Prefix                *string     `xml:"Prefix"`
	Delimiter             *string     `xml:"Delimiter"`
	MaxKeys               *int        `xml:"MaxKeys"`
	KeyCount              *int        `xml:"KeyCount"`
	IsTruncated           *bool       `xml:"IsTruncated"`
	EncodingType          *string     `xml:"EncodingType"`
	ContinuationToken     *string     `xml:"ContinuationToken"`
	NextContinuationToken *string     `xml:"NextContinuationToken"`
	StartAfter            *string     `xml:"StartAfter"`
	Contents              []objectXML `xml:"Contents"`
	CommonPrefixes        []anyEl     `xml:"CommonPrefixes"`
	Extra                 []anyEl     `xml:",any"`
}

func decodeXML(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := xml.Unmarshal(raw, v); err != nil {
		failf(t, "invalid XML: %v\n%s", err, raw)
	}
}

func noExtra(t *testing.T, where string, extra []anyEl) {
	t.Helper()
	if len(extra) == 0 {
		return
	}
	var names []string
	for _, e := range extra {
		names = append(names, e.XMLName.Local)
	}
	failf(t, "%s contains unexpected elements: %v", where, names)
}

func requireNS(t *testing.T, n xml.Name) {
	t.Helper()
	if n.Space != s3NS {
		failf(t, "root <%s> has namespace %q, want %q", n.Local, n.Space, s3NS)
	}
}

// =====================================================================
// Wire-level assertions
// =====================================================================

type bodyKind int

const (
	kindXML   bodyKind = iota // XML document with declaration
	kindEmpty                 // no body at all
	kindRaw                   // arbitrary bytes (object data)
)

var seenIDs sync.Map

func assertResponse(t *testing.T, ex exchange, status int, kind bodyKind) {
	t.Helper()
	where := ex.Method + " " + ex.Path
	if ex.Status != status {
		failf(t, "%s: expected HTTP %d, got %d\nbody: %s", where, status, ex.Status, ex.Body)
	}

	id := ex.Header.Get("X-Amz-Request-Id")
	if id == "" {
		failf(t, "%s: missing x-amz-request-id", where)
	}
	if _, dup := seenIDs.LoadOrStore(id, true); dup {
		failf(t, "%s: x-amz-request-id %q was already used by another response", where, id)
	}
	if ex.Header.Get("X-Amz-Id-2") == "" {
		failf(t, "%s: missing x-amz-id-2", where)
	}
	if ex.Header.Get("Date") == "" {
		failf(t, "%s: missing Date", where)
	}
	if cl := ex.Header.Get("Content-Length"); cl != "" && ex.Method != http.MethodHead {
		if cl != strconv.Itoa(len(ex.Body)) {
			failf(t, "%s: Content-Length %s does not match body length %d", where, cl, len(ex.Body))
		}
	}

	switch kind {
	case kindXML:
		mt, _, _ := mime.ParseMediaType(ex.Header.Get("Content-Type"))
		if mt != "application/xml" {
			failf(t, "%s: Content-Type %q, want application/xml", where, ex.Header.Get("Content-Type"))
		}
		if !bytes.HasPrefix(ex.Body, []byte(xmlDecl)) {
			failf(t, "%s: body must start with the XML declaration, got %.60q", where, ex.Body)
		}
	case kindEmpty:
		if len(ex.Body) != 0 {
			failf(t, "%s: expected empty body, got %d bytes: %.200q", where, len(ex.Body), ex.Body)
		}
	}
}

// assertAPIError validates an S3 <Error> response. extras lists the
// optional fields that must be present (BucketName / Key) with exact values.
func assertAPIError(t *testing.T, ex exchange, status int, code string, extras map[string]string) {
	t.Helper()
	assertResponse(t, ex, status, kindXML)

	var e errorXML
	decodeXML(t, ex.Body, &e)
	if e.XMLName.Space != "" {
		failf(t, "<Error> must have no namespace, got %q", e.XMLName.Space)
	}
	noExtra(t, "<Error>", e.Extra)

	if e.Code == nil || e.Message == nil || e.RequestID == nil || e.HostID == nil {
		failf(t, "<Error> must contain Code, Message, RequestId and HostId:\n%s", ex.Body)
	}
	if *e.Code != code {
		failf(t, "error Code %q, want %q", *e.Code, code)
	}
	if *e.Message == "" {
		fail(t, "error Message is empty")
	}
	if *e.RequestID != ex.Header.Get("X-Amz-Request-Id") {
		failf(t, "body RequestId %q != header x-amz-request-id %q", *e.RequestID, ex.Header.Get("X-Amz-Request-Id"))
	}
	if *e.HostID != ex.Header.Get("X-Amz-Id-2") {
		failf(t, "body HostId %q != header x-amz-id-2 %q", *e.HostID, ex.Header.Get("X-Amz-Id-2"))
	}

	got := map[string]string{}
	if e.BucketName != nil {
		got["BucketName"] = *e.BucketName
	}
	if e.Key != nil {
		got["Key"] = *e.Key
	}
	for k, want := range extras {
		if g, ok := got[k]; !ok || g != want {
			failf(t, "error field %s = %q (present=%v), want %q", k, g, ok, want)
		}
	}
	for k := range got {
		if _, ok := extras[k]; !ok {
			failf(t, "error contains unexpected field %s", k)
		}
	}
}

// requireSDKError checks what the SDK made of the failure.
func requireSDKError(t *testing.T, err error, ex exchange, code string) {
	t.Helper()
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		failf(t, "error is not a smithy.APIError: %T %v", err, err)
	}
	if ae.ErrorCode() != code {
		failf(t, "SDK error code %q, want %q (err: %v)", ae.ErrorCode(), code, err)
	}
	var re *awshttp.ResponseError
	if !errors.As(err, &re) {
		failf(t, "error carries no HTTP response: %v", err)
	}
	if re.HTTPStatusCode() != ex.Status {
		failf(t, "SDK status %d != wire status %d", re.HTTPStatusCode(), ex.Status)
	}
	if re.ServiceRequestID() != ex.Header.Get("X-Amz-Request-Id") {
		failf(t, "SDK request id %q != header %q", re.ServiceRequestID(), ex.Header.Get("X-Amz-Request-Id"))
	}
}

func md5ETag(b []byte) string {
	s := md5.Sum(b)
	return `"` + hex.EncodeToString(s[:]) + `"`
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// assertObjectHeaders validates the metadata headers shared by GET and HEAD.
func assertObjectHeaders(t *testing.T, ex exchange, size int, etag, ctype string) {
	t.Helper()
	h := ex.Header
	if got := h.Get("ETag"); got != etag {
		failf(t, "ETag %q, want %q", got, etag)
	}
	if !etagRe.MatchString(h.Get("ETag")) {
		failf(t, "ETag %q must be a quoted 32-char hex md5", h.Get("ETag"))
	}
	if got := h.Get("Content-Length"); got != strconv.Itoa(size) {
		failf(t, "Content-Length %q, want %d", got, size)
	}
	if got := h.Get("Content-Type"); got != ctype {
		failf(t, "Content-Type %q, want %q", got, ctype)
	}
	if got := h.Get("Accept-Ranges"); got != "bytes" {
		failf(t, "Accept-Ranges %q, want bytes", got)
	}
	lm, err := time.Parse(http.TimeFormat, h.Get("Last-Modified"))
	if err != nil {
		failf(t, "Last-Modified %q is not RFC 7231 format: %v", h.Get("Last-Modified"), err)
	}
	if time.Since(lm) > 10*time.Minute || time.Until(lm) > time.Minute {
		failf(t, "Last-Modified %v is not close to now", lm)
	}
	if h.Get("Content-Range") != "" {
		fail(t, "Content-Range must not be set on a full 200 response")
	}
}

// =====================================================================
// ListObjectsV2 shared validation (wire XML + SDK view)
// =====================================================================

type wantObject struct {
	Key  string
	Size int64
	ETag string
}

func sortedWant(w []wantObject) []wantObject {
	out := append([]wantObject(nil), w...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (h *harness) listAndAssert(t *testing.T, bucket string, want []wantObject) {
	t.Helper()
	want = sortedWant(want)

	h.rec.reset()
	out, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		failf(t, "ListObjectsV2: %v", err)
	}
	ex := h.rec.one(t)
	assertResponse(t, ex, http.StatusOK, kindXML)

	var doc listObjectsXML
	decodeXML(t, ex.Body, &doc)
	requireNS(t, doc.XMLName)
	noExtra(t, "ListBucketResult", doc.Extra)

	if doc.Name == nil || doc.Prefix == nil || doc.MaxKeys == nil || doc.KeyCount == nil || doc.IsTruncated == nil {
		failf(t, "ListBucketResult must contain Name, Prefix, MaxKeys, KeyCount, IsTruncated:\n%s", ex.Body)
	}
	if *doc.Name != bucket {
		failf(t, "Name %q, want %q", *doc.Name, bucket)
	}
	if *doc.Prefix != "" {
		failf(t, "Prefix %q, want empty", *doc.Prefix)
	}
	if *doc.MaxKeys != 1000 {
		failf(t, "MaxKeys %d, want 1000", *doc.MaxKeys)
	}
	if *doc.KeyCount != len(want) {
		failf(t, "KeyCount %d, want %d", *doc.KeyCount, len(want))
	}
	if *doc.IsTruncated {
		fail(t, "IsTruncated must be false")
	}
	for name, v := range map[string]*string{
		"Delimiter": doc.Delimiter, "EncodingType": doc.EncodingType,
		"ContinuationToken": doc.ContinuationToken, "NextContinuationToken": doc.NextContinuationToken,
		"StartAfter": doc.StartAfter,
	} {
		if v != nil {
			failf(t, "%s must be omitted when not requested, got %q", name, *v)
		}
	}
	if len(doc.CommonPrefixes) != 0 {
		fail(t, "CommonPrefixes must be omitted when no delimiter was sent")
	}
	if len(doc.Contents) != len(want) {
		failf(t, "Contents count %d, want %d\n%s", len(doc.Contents), len(want), ex.Body)
	}
	for i, c := range doc.Contents {
		noExtra(t, "Contents", c.Extra)
		if c.Key == nil || c.LastModified == nil || c.ETag == nil || c.Size == nil || c.StorageClass == nil {
			failf(t, "Contents[%d] must contain Key, LastModified, ETag, Size, StorageClass", i)
		}
		if *c.Key != want[i].Key {
			failf(t, "Contents[%d].Key %q, want %q (keys must be sorted, unencoded)", i, *c.Key, want[i].Key)
		}
		if *c.ETag != want[i].ETag {
			failf(t, "Contents[%d].ETag %q, want %q", i, *c.ETag, want[i].ETag)
		}
		if *c.Size != want[i].Size {
			failf(t, "Contents[%d].Size %d, want %d", i, *c.Size, want[i].Size)
		}
		if *c.StorageClass != "STANDARD" {
			failf(t, "Contents[%d].StorageClass %q, want STANDARD", i, *c.StorageClass)
		}
		if !isoRe.MatchString(*c.LastModified) {
			failf(t, "Contents[%d].LastModified %q must match 2006-01-02T15:04:05.000Z", i, *c.LastModified)
		}
	}

	// What the SDK decoded.
	if aws.ToString(out.Name) != bucket || aws.ToString(out.Prefix) != "" {
		failf(t, "SDK Name/Prefix = %q/%q", aws.ToString(out.Name), aws.ToString(out.Prefix))
	}
	if aws.ToInt32(out.MaxKeys) != 1000 || int(aws.ToInt32(out.KeyCount)) != len(want) {
		failf(t, "SDK MaxKeys/KeyCount = %d/%d", aws.ToInt32(out.MaxKeys), aws.ToInt32(out.KeyCount))
	}
	if aws.ToBool(out.IsTruncated) || out.ContinuationToken != nil || out.NextContinuationToken != nil {
		fail(t, "SDK reports truncation or tokens")
	}
	if len(out.CommonPrefixes) != 0 {
		fail(t, "SDK reports CommonPrefixes")
	}
	if len(out.Contents) != len(want) {
		failf(t, "SDK Contents %d, want %d", len(out.Contents), len(want))
	}
	for i, o := range out.Contents {
		if aws.ToString(o.Key) != want[i].Key || aws.ToString(o.ETag) != want[i].ETag ||
			aws.ToInt64(o.Size) != want[i].Size {
			failf(t, "SDK Contents[%d] mismatch: %s %s %d", i, aws.ToString(o.Key), aws.ToString(o.ETag), aws.ToInt64(o.Size))
		}
		if o.StorageClass != types.ObjectStorageClassStandard {
			failf(t, "SDK Contents[%d].StorageClass %q", i, o.StorageClass)
		}
		if o.LastModified == nil || o.LastModified.IsZero() {
			failf(t, "SDK Contents[%d].LastModified missing", i)
		}
	}
}

// =====================================================================
// 1. ListBuckets   GET /
// =====================================================================

func TestListBuckets(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	b1, b2 := h.newBucket(t), h.newBucket(t)

	h.rec.reset()
	out, err := h.client.ListBuckets(bg, &s3.ListBucketsInput{})
	if err != nil {
		failf(t, "ListBuckets: %v", err)
	}
	ex := h.rec.one(t)
	assertResponse(t, ex, http.StatusOK, kindXML)

	var doc listBucketsXML
	decodeXML(t, ex.Body, &doc)
	requireNS(t, doc.XMLName)
	noExtra(t, "ListAllMyBucketsResult", doc.Extra)

	if doc.Owner == nil || doc.Owner.ID == nil || doc.Owner.DisplayName == nil {
		failf(t, "Owner must contain ID and DisplayName:\n%s", ex.Body)
	}
	if *doc.Owner.ID == "" || *doc.Owner.DisplayName == "" {
		fail(t, "Owner ID/DisplayName must not be empty")
	}
	noExtra(t, "Owner", doc.Owner.Extra)

	// <Buckets> must always be present (even for an account with no buckets).
	if doc.Buckets == nil {
		failf(t, "<Buckets> element is missing:\n%s", ex.Body)
	}
	noExtra(t, "Buckets", doc.Buckets.Extra)

	names := map[string]bool{}
	for i, b := range doc.Buckets.Bucket {
		noExtra(t, "Bucket", b.Extra)
		if b.Name == nil || b.CreationDate == nil || *b.Name == "" {
			failf(t, "Bucket[%d] must contain Name and CreationDate", i)
		}
		if !isoRe.MatchString(*b.CreationDate) {
			failf(t, "Bucket[%d].CreationDate %q must match 2006-01-02T15:04:05.000Z", i, *b.CreationDate)
		}
		names[*b.Name] = true
	}
	if !names[b1] || !names[b2] {
		failf(t, "created buckets %s, %s not listed (got %d buckets)", b1, b2, len(names))
	}

	// SDK view.
	if out.Owner == nil || aws.ToString(out.Owner.ID) != *doc.Owner.ID ||
		aws.ToString(out.Owner.DisplayName) != *doc.Owner.DisplayName {
		fail(t, "SDK Owner does not match wire Owner")
	}
	if len(out.Buckets) != len(doc.Buckets.Bucket) {
		failf(t, "SDK bucket count %d != wire %d", len(out.Buckets), len(doc.Buckets.Bucket))
	}
	for i, b := range out.Buckets {
		if aws.ToString(b.Name) != *doc.Buckets.Bucket[i].Name {
			failf(t, "SDK Bucket[%d].Name mismatch", i)
		}
		if b.CreationDate == nil || b.CreationDate.IsZero() {
			failf(t, "SDK Bucket[%d].CreationDate missing", i)
		}
	}
}

// =====================================================================
// 2. CreateBucket   PUT /{bucket}
// =====================================================================

func TestCreateBucket(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("success", func(t *testing.T) {
		colorTestResult(t)
		name := uniqueBucket()
		t.Cleanup(func() { h.purge(name) })

		h.rec.reset()
		out, err := h.client.CreateBucket(bg, &s3.CreateBucketInput{Bucket: aws.String(name)})
		if err != nil {
			failf(t, "CreateBucket: %v", err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindEmpty)
		if got := ex.Header.Get("Location"); got != "/"+name {
			failf(t, "Location %q, want %q", got, "/"+name)
		}
		if aws.ToString(out.Location) != "/"+name {
			failf(t, "SDK Location %q, want %q", aws.ToString(out.Location), "/"+name)
		}

		// The bucket now exists.
		ex = h.ok(t, func() error {
			_, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertResponse(t, ex, http.StatusOK, kindEmpty)
	})

	t.Run("already owned by you", func(t *testing.T) {
		colorTestResult(t)
		name := h.newBucket(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.CreateBucket(bg, &s3.CreateBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertAPIError(t, ex, http.StatusConflict, "BucketAlreadyOwnedByYou", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "BucketAlreadyOwnedByYou")
		var typed *types.BucketAlreadyOwnedByYou
		if !errors.As(err, &typed) {
			failf(t, "SDK did not map to *types.BucketAlreadyOwnedByYou: %T", err)
		}
	})

	t.Run("invalid names", func(t *testing.T) {
		colorTestResult(t)
		for _, name := range []string{
			"ab",                    // too short
			strings.Repeat("a", 64), // too long
			"UPPERCASE-BUCKET",      // uppercase
			"has_underscore",        // underscore
			"-leading-hyphen",       // leading hyphen
			"trailing-hyphen-",      // trailing hyphen
			"192.168.5.4",           // IP address format
			"double..dot",           // adjacent periods
		} {
			t.Run(name, func(t *testing.T) {
				colorTestResult(t)
				t.Cleanup(func() { h.purge(name) })
				ex, err := h.failing(t, func() error {
					_, err := h.client.CreateBucket(bg, &s3.CreateBucketInput{Bucket: aws.String(name)})
					return err
				})
				assertAPIError(t, ex, http.StatusBadRequest, "InvalidBucketName", map[string]string{"BucketName": name})
				requireSDKError(t, err, ex, "InvalidBucketName")
			})
		}
	})
}

// =====================================================================
// 3. HeadBucket   HEAD /{bucket}
// =====================================================================

func TestHeadBucket(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("exists", func(t *testing.T) {
		colorTestResult(t)
		name := h.newBucket(t)
		h.rec.reset()
		out, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(name)})
		if err != nil {
			failf(t, "HeadBucket: %v", err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindEmpty)

		region := ex.Header.Get("X-Amz-Bucket-Region")
		if region == "" {
			fail(t, "missing x-amz-bucket-region header")
		}
		if aws.ToString(out.BucketRegion) != region {
			failf(t, "SDK BucketRegion %q != header %q", aws.ToString(out.BucketRegion), region)
		}
	})

	t.Run("missing bucket: status only", func(t *testing.T) {
		colorTestResult(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(uniqueBucket())})
			return err
		})
		assertResponse(t, ex, http.StatusNotFound, kindEmpty)
		requireSDKError(t, err, ex, "NotFound")
		var nf *types.NotFound
		if !errors.As(err, &nf) {
			failf(t, "SDK did not map to *types.NotFound: %T", err)
		}
	})
}

// =====================================================================
// 4. DeleteBucket   DELETE /{bucket}
// =====================================================================

func TestDeleteBucket(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("success", func(t *testing.T) {
		colorTestResult(t)
		name := h.newBucket(t)
		ex := h.ok(t, func() error {
			_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertResponse(t, ex, http.StatusNoContent, kindEmpty)

		// Gone: Head says 404, ListBuckets no longer shows it.
		hex, _ := h.failing(t, func() error {
			_, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertResponse(t, hex, http.StatusNotFound, kindEmpty)

		out, err := h.client.ListBuckets(bg, &s3.ListBucketsInput{})
		if err != nil {
			fail(t, err)
		}
		for _, b := range out.Buckets {
			if aws.ToString(b.Name) == name {
				fail(t, "deleted bucket still listed")
			}
		}
	})

	t.Run("missing bucket", func(t *testing.T) {
		colorTestResult(t)
		name := uniqueBucket()
		ex, err := h.failing(t, func() error {
			_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "NoSuchBucket")
	})

	t.Run("deleting twice", func(t *testing.T) {
		colorTestResult(t)
		name := h.newBucket(t)
		h.ok(t, func() error {
			_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
			return err
		})
		ex, err := h.failing(t, func() error {
			_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "NoSuchBucket")
	})

	t.Run("not empty", func(t *testing.T) {
		colorTestResult(t)
		name := h.newBucket(t)
		content := []byte("keep me")
		h.put(t, name, "keep.txt", content, "text/plain")

		ex, err := h.failing(t, func() error {
			_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertAPIError(t, ex, http.StatusConflict, "BucketNotEmpty", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "BucketNotEmpty")

		// Nothing was lost.
		h.ok(t, func() error {
			_, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(name)})
			return err
		})
		_, _, body := h.get(t, name, "keep.txt")
		if !bytes.Equal(body, content) {
			fail(t, "object changed after failed DeleteBucket")
		}

		// Once emptied, the delete succeeds.
		h.ok(t, func() error {
			_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(name), Key: aws.String("keep.txt")})
			return err
		})
		ex = h.ok(t, func() error {
			_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertResponse(t, ex, http.StatusNoContent, kindEmpty)
	})
}

// =====================================================================
// 5. ListObjectsV2   GET /{bucket}?list-type=2
// =====================================================================

func TestListObjectsV2(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("empty bucket", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		h.listAndAssert(t, bucket, nil)
	})

	t.Run("populated, sorted, matches uploads", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		var want []wantObject
		for _, o := range []struct {
			key  string
			body []byte
		}{
			{"zeta.txt", []byte("zeta")},
			{"alpha.txt", []byte("alpha content")},
			{"mid/dle.txt", randBytes(4096)},
			{"empty.bin", nil},
			{"Alpha-upper.txt", []byte("x")},
		} {
			etag := h.put(t, bucket, o.key, o.body, "")
			want = append(want, wantObject{o.key, int64(len(o.body)), etag})
		}
		h.listAndAssert(t, bucket, want)
	})

	t.Run("reflects overwrite and delete", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		h.put(t, bucket, "a.txt", []byte("one"), "")
		etagB := h.put(t, bucket, "b.txt", []byte("bbb"), "")
		etagA2 := h.put(t, bucket, "a.txt", []byte("two-longer"), "")
		h.listAndAssert(t, bucket, []wantObject{
			{"a.txt", 10, etagA2},
			{"b.txt", 3, etagB},
		})

		h.ok(t, func() error {
			_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("a.txt")})
			return err
		})
		h.listAndAssert(t, bucket, []wantObject{{"b.txt", 3, etagB}})
	})

	t.Run("missing bucket", func(t *testing.T) {
		colorTestResult(t)
		name := uniqueBucket()
		ex, err := h.failing(t, func() error {
			_, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{Bucket: aws.String(name)})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "NoSuchBucket")
		var typed *types.NoSuchBucket
		if !errors.As(err, &typed) {
			failf(t, "SDK did not map to *types.NoSuchBucket: %T", err)
		}
	})
}

// =====================================================================
// 6. PutObject   PUT /{bucket}/{key...}
// =====================================================================

func TestPutObject(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)

	put := func(t *testing.T, key string, body []byte, ctype string) (exchange, *s3.PutObjectOutput) {
		t.Helper()
		in := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body)}
		if ctype != "" {
			in.ContentType = aws.String(ctype)
		}
		h.rec.reset()
		out, err := h.client.PutObject(bg, in)
		if err != nil {
			failf(t, "PutObject(%s): %v", key, err)
		}
		ex := h.rec.one(t)
		assertResponse(t, ex, http.StatusOK, kindEmpty)
		want := md5ETag(body)
		if got := ex.Header.Get("ETag"); got != want {
			failf(t, "ETag header %q, want md5 %q", got, want)
		}
		if aws.ToString(out.ETag) != want {
			failf(t, "SDK ETag %q, want %q", aws.ToString(out.ETag), want)
		}
		return ex, out
	}

	t.Run("text with content type", func(t *testing.T) {
		colorTestResult(t)
		content := []byte("Hello from AWS SDK to ownS3!")
		put(t, "hello.txt", content, "text/plain")
		ex, _, body := h.get(t, bucket, "hello.txt")
		assertObjectHeaders(t, ex, len(content), md5ETag(content), "text/plain")
		if !bytes.Equal(body, content) {
			fail(t, "downloaded bytes differ from uploaded bytes")
		}
	})

	t.Run("default content type", func(t *testing.T) {
		colorTestResult(t)
		put(t, "no-type.bin", []byte("data"), "")
		ex, out := h.head(t, bucket, "no-type.bin")
		if got := ex.Header.Get("Content-Type"); got != "binary/octet-stream" {
			failf(t, "default Content-Type %q, want binary/octet-stream", got)
		}
		if aws.ToString(out.ContentType) != "binary/octet-stream" {
			failf(t, "SDK ContentType %q", aws.ToString(out.ContentType))
		}
	})

	t.Run("zero-byte object", func(t *testing.T) {
		colorTestResult(t)
		put(t, "empty.txt", nil, "text/plain")
		ex, _, body := h.get(t, bucket, "empty.txt")
		assertObjectHeaders(t, ex, 0, `"d41d8cd98f00b204e9800998ecf8427e"`, "text/plain")
		if len(body) != 0 {
			failf(t, "expected empty body, got %d bytes", len(body))
		}
	})

	t.Run("1 MiB random binary", func(t *testing.T) {
		colorTestResult(t)
		content := randBytes(1 << 20)
		put(t, "random.bin", content, "application/octet-stream")
		ex, _, body := h.get(t, bucket, "random.bin")
		assertObjectHeaders(t, ex, len(content), md5ETag(content), "application/octet-stream")
		if !bytes.Equal(body, content) {
			fail(t, "binary round-trip corrupted the data")
		}
	})

	t.Run("overwrite replaces content, size and ETag", func(t *testing.T) {
		colorTestResult(t)
		first, second := []byte("first"), []byte("second version, longer")
		put(t, "over.txt", first, "text/plain")
		put(t, "over.txt", second, "text/html")
		ex, _, body := h.get(t, bucket, "over.txt")
		assertObjectHeaders(t, ex, len(second), md5ETag(second), "text/html")
		if !bytes.Equal(body, second) {
			fail(t, "overwrite did not replace the content")
		}
	})

	t.Run("key shapes", func(t *testing.T) {
		colorTestResult(t)
		kb := h.newBucket(t)
		keys := []string{
			"dir/nested/deep/file.txt",
			"with space.txt",
			"plus+sign.txt",
			"100%.txt",
			"percent%20literal.txt",
			"unicode/日本語.txt",
			"ünï-côdé.txt",
			"UPPER.TXT",
			"dash_under-score.v1.0.txt",
			"tilde~(paren)=eq&amp.txt",
			strings.Repeat("k", 1024),
		}
		var want []wantObject
		for _, key := range keys {
			content := []byte("content of " + key[:min(len(key), 20)])
			etag := h.put(t, kb, key, content, "text/plain")

			ex, _, body := h.get(t, kb, key)
			assertObjectHeaders(t, ex, len(content), etag, "text/plain")
			if !bytes.Equal(body, content) {
				failf(t, "key %q: round-trip mismatch", key)
			}
			hex, _ := h.head(t, kb, key)
			assertResponse(t, hex, http.StatusOK, kindEmpty)

			want = append(want, wantObject{key, int64(len(content)), etag})
		}
		h.listAndAssert(t, kb, want)

		for _, key := range keys {
			h.ok(t, func() error {
				_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(kb), Key: aws.String(key)})
				return err
			})
			ex, err := h.failing(t, func() error {
				_, err := h.client.GetObject(bg, &s3.GetObjectInput{Bucket: aws.String(kb), Key: aws.String(key)})
				return err
			})
			// Key in the error must be echoed exactly, unencoded.
			assertAPIError(t, ex, http.StatusNotFound, "NoSuchKey", map[string]string{"Key": key})
			requireSDKError(t, err, ex, "NoSuchKey")
		}
		h.listAndAssert(t, kb, nil)
	})

	t.Run("missing bucket", func(t *testing.T) {
		colorTestResult(t)
		name := uniqueBucket()
		ex, err := h.failing(t, func() error {
			_, err := h.client.PutObject(bg, &s3.PutObjectInput{
				Bucket: aws.String(name), Key: aws.String("k"), Body: strings.NewReader("x"),
			})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "NoSuchBucket")

		// A failed put must not create the bucket.
		hx, _ := h.failing(t, func() error {
			_, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(name)})
			return err
		})
		assertResponse(t, hx, http.StatusNotFound, kindEmpty)
	})
}

// =====================================================================
// 7. GetObject   GET /{bucket}/{key...}
// =====================================================================

func TestGetObject(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	content := []byte("Hello from AWS SDK to ownS3!")
	etag := h.put(t, bucket, "hello.txt", content, "text/plain")

	t.Run("success", func(t *testing.T) {
		colorTestResult(t)
		ex, out, body := h.get(t, bucket, "hello.txt")
		assertResponse(t, ex, http.StatusOK, kindRaw)
		assertObjectHeaders(t, ex, len(content), etag, "text/plain")
		if !bytes.Equal(ex.Body, content) || !bytes.Equal(body, content) {
			fail(t, "body bytes differ from uploaded bytes")
		}

		if aws.ToInt64(out.ContentLength) != int64(len(content)) {
			failf(t, "SDK ContentLength %d", aws.ToInt64(out.ContentLength))
		}
		if aws.ToString(out.ContentType) != "text/plain" {
			failf(t, "SDK ContentType %q", aws.ToString(out.ContentType))
		}
		if aws.ToString(out.ETag) != etag {
			failf(t, "SDK ETag %q, want %q", aws.ToString(out.ETag), etag)
		}
		if aws.ToString(out.AcceptRanges) != "bytes" {
			failf(t, "SDK AcceptRanges %q", aws.ToString(out.AcceptRanges))
		}
		if out.LastModified == nil || out.LastModified.IsZero() {
			fail(t, "SDK LastModified missing")
		}
	})

	t.Run("repeatable and stable", func(t *testing.T) {
		colorTestResult(t)
		ex1, _, _ := h.get(t, bucket, "hello.txt")
		ex2, _, _ := h.get(t, bucket, "hello.txt")
		for _, hd := range []string{"ETag", "Last-Modified", "Content-Length", "Content-Type"} {
			if ex1.Header.Get(hd) != ex2.Header.Get(hd) {
				failf(t, "%s changed between two GETs of the same object", hd)
			}
		}
	})

	t.Run("missing key", func(t *testing.T) {
		colorTestResult(t)
		ex, err := h.failing(t, func() error {
			out, err := h.client.GetObject(bg, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("nope.txt")})
			if out != nil && out.Body != nil {
				out.Body.Close()
			}
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchKey", map[string]string{"Key": "nope.txt"})
		requireSDKError(t, err, ex, "NoSuchKey")
		var typed *types.NoSuchKey
		if !errors.As(err, &typed) {
			failf(t, "SDK did not map to *types.NoSuchKey: %T", err)
		}
	})

	t.Run("missing bucket", func(t *testing.T) {
		colorTestResult(t)
		name := uniqueBucket()
		ex, err := h.failing(t, func() error {
			out, err := h.client.GetObject(bg, &s3.GetObjectInput{Bucket: aws.String(name), Key: aws.String("k")})
			if out != nil && out.Body != nil {
				out.Body.Close()
			}
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "NoSuchBucket")
	})
}

// =====================================================================
// 8. HeadObject   HEAD /{bucket}/{key...}
// =====================================================================

func TestHeadObject(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	bucket := h.newBucket(t)
	content := []byte("Hello from AWS SDK to ownS3!")
	etag := h.put(t, bucket, "hello.txt", content, "text/plain")
	h.put(t, bucket, "empty.txt", nil, "text/plain")

	t.Run("success: headers only", func(t *testing.T) {
		colorTestResult(t)
		ex, out := h.head(t, bucket, "hello.txt")
		assertResponse(t, ex, http.StatusOK, kindEmpty)
		// Content-Length describes the object, even though no body is sent.
		assertObjectHeaders(t, ex, len(content), etag, "text/plain")

		if aws.ToInt64(out.ContentLength) != int64(len(content)) {
			failf(t, "SDK ContentLength %d", aws.ToInt64(out.ContentLength))
		}
		if aws.ToString(out.ContentType) != "text/plain" || aws.ToString(out.ETag) != etag {
			failf(t, "SDK ContentType/ETag = %q/%q", aws.ToString(out.ContentType), aws.ToString(out.ETag))
		}
		if aws.ToString(out.AcceptRanges) != "bytes" {
			failf(t, "SDK AcceptRanges %q", aws.ToString(out.AcceptRanges))
		}
		if out.LastModified == nil || out.LastModified.IsZero() {
			fail(t, "SDK LastModified missing")
		}
	})

	t.Run("identical metadata to GET", func(t *testing.T) {
		colorTestResult(t)
		hx, _ := h.head(t, bucket, "hello.txt")
		gx, _, _ := h.get(t, bucket, "hello.txt")
		for _, hd := range []string{"ETag", "Last-Modified", "Content-Length", "Content-Type", "Accept-Ranges"} {
			if hx.Header.Get(hd) != gx.Header.Get(hd) {
				failf(t, "%s differs: HEAD=%q GET=%q", hd, hx.Header.Get(hd), gx.Header.Get(hd))
			}
		}
	})

	t.Run("zero-byte object", func(t *testing.T) {
		colorTestResult(t)
		ex, out := h.head(t, bucket, "empty.txt")
		assertResponse(t, ex, http.StatusOK, kindEmpty)
		assertObjectHeaders(t, ex, 0, `"d41d8cd98f00b204e9800998ecf8427e"`, "text/plain")
		if aws.ToInt64(out.ContentLength) != 0 {
			failf(t, "SDK ContentLength %d, want 0", aws.ToInt64(out.ContentLength))
		}
	})

	t.Run("missing key: status only", func(t *testing.T) {
		colorTestResult(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.HeadObject(bg, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("nope.txt")})
			return err
		})
		assertResponse(t, ex, http.StatusNotFound, kindEmpty)
		requireSDKError(t, err, ex, "NotFound")
		var nf *types.NotFound
		if !errors.As(err, &nf) {
			failf(t, "SDK did not map to *types.NotFound: %T", err)
		}
	})

	t.Run("missing bucket: status only", func(t *testing.T) {
		colorTestResult(t)
		ex, err := h.failing(t, func() error {
			_, err := h.client.HeadObject(bg, &s3.HeadObjectInput{Bucket: aws.String(uniqueBucket()), Key: aws.String("k")})
			return err
		})
		assertResponse(t, ex, http.StatusNotFound, kindEmpty)
		requireSDKError(t, err, ex, "NotFound")
	})
}

// =====================================================================
// 9. DeleteObject   DELETE /{bucket}/{key...}
// =====================================================================

func TestDeleteObject(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)

	t.Run("success and isolation", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		keepETag := h.put(t, bucket, "keep.txt", []byte("keep"), "")
		h.put(t, bucket, "remove.txt", []byte("remove"), "")

		ex := h.ok(t, func() error {
			_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("remove.txt")})
			return err
		})
		assertResponse(t, ex, http.StatusNoContent, kindEmpty)

		// Gone for GET and HEAD.
		gx, gerr := h.failing(t, func() error {
			out, err := h.client.GetObject(bg, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("remove.txt")})
			if out != nil && out.Body != nil {
				out.Body.Close()
			}
			return err
		})
		assertAPIError(t, gx, http.StatusNotFound, "NoSuchKey", map[string]string{"Key": "remove.txt"})
		requireSDKError(t, gerr, gx, "NoSuchKey")

		hx, herr := h.failing(t, func() error {
			_, err := h.client.HeadObject(bg, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("remove.txt")})
			return err
		})
		assertResponse(t, hx, http.StatusNotFound, kindEmpty)
		requireSDKError(t, herr, hx, "NotFound")

		// The other object is untouched and the listing agrees.
		h.listAndAssert(t, bucket, []wantObject{{"keep.txt", 4, keepETag}})
	})

	t.Run("idempotent: missing key returns 204", func(t *testing.T) {
		colorTestResult(t)
		bucket := h.newBucket(t)
		for i := 0; i < 2; i++ {
			ex := h.ok(t, func() error {
				_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("never-existed.txt")})
				return err
			})
			assertResponse(t, ex, http.StatusNoContent, kindEmpty)
		}

		h.put(t, bucket, "once.txt", []byte("x"), "")
		for i := 0; i < 2; i++ {
			ex := h.ok(t, func() error {
				_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("once.txt")})
				return err
			})
			assertResponse(t, ex, http.StatusNoContent, kindEmpty)
		}
	})

	t.Run("missing bucket", func(t *testing.T) {
		colorTestResult(t)
		name := uniqueBucket()
		ex, err := h.failing(t, func() error {
			_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(name), Key: aws.String("k")})
			return err
		})
		assertAPIError(t, ex, http.StatusNotFound, "NoSuchBucket", map[string]string{"BucketName": name})
		requireSDKError(t, err, ex, "NoSuchBucket")
	})
}

// =====================================================================
// Unsupported methods: even routing failures must be S3-style errors.
// =====================================================================

func (h *harness) raw(t *testing.T, method, path string) exchange {
	t.Helper()
	req, err := http.NewRequest(method, h.base+path, nil)
	if err != nil {
		fail(t, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		failf(t, "%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return exchange{Method: method, Path: path, Status: resp.StatusCode, Header: resp.Header, Body: body}
}

func TestUnsupportedMethods(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	for _, c := range []struct{ method, path string }{
		{"PUT", "/"},
		{"DELETE", "/"},
		{"POST", "/"},
		{"PATCH", "/"},
		{"PATCH", "/some-bucket"},
		{"PATCH", "/some-bucket/some/key.txt"},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			colorTestResult(t)
			ex := h.raw(t, c.method, c.path)
			assertAPIError(t, ex, http.StatusMethodNotAllowed, "MethodNotAllowed", nil)
		})
	}

	t.Run("HEAD / has no body", func(t *testing.T) {
		colorTestResult(t)
		ex := h.raw(t, "HEAD", "/")
		assertResponse(t, ex, http.StatusMethodNotAllowed, kindEmpty)
	})
}

// =====================================================================
// End-to-end lifecycle through the SDK (state transitions across routes)
// =====================================================================

func TestLifecycle(t *testing.T) {
	colorTestResult(t)
	h := newHarness(t)
	name := uniqueBucket()
	t.Cleanup(func() { h.purge(name) })
	content := []byte("lifecycle body")
	key := "life/cycle.txt"

	ok := func(op func() error) exchange { t.Helper(); return h.ok(t, op) }
	bad := func(op func() error) exchange { t.Helper(); ex, _ := h.failing(t, op); return ex }

	assertResponse(t, ok(func() error {
		_, err := h.client.CreateBucket(bg, &s3.CreateBucketInput{Bucket: aws.String(name)})
		return err
	}), 200, kindEmpty)

	assertResponse(t, ok(func() error {
		_, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(name)})
		return err
	}), 200, kindEmpty)

	h.listAndAssert(t, name, nil)

	etag := h.put(t, name, key, content, "text/plain")
	h.listAndAssert(t, name, []wantObject{{key, int64(len(content)), etag}})

	gx, _, body := h.get(t, name, key)
	assertObjectHeaders(t, gx, len(content), etag, "text/plain")
	if !bytes.Equal(body, content) {
		fail(t, "lifecycle body mismatch")
	}

	// bucket cannot be deleted while it holds the object
	assertAPIError(t, bad(func() error {
		_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
		return err
	}), 409, "BucketNotEmpty", map[string]string{"BucketName": name})

	assertResponse(t, ok(func() error {
		_, err := h.client.DeleteObject(bg, &s3.DeleteObjectInput{Bucket: aws.String(name), Key: aws.String(key)})
		return err
	}), 204, kindEmpty)
	h.listAndAssert(t, name, nil)

	assertResponse(t, ok(func() error {
		_, err := h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(name)})
		return err
	}), 204, kindEmpty)

	assertResponse(t, bad(func() error {
		_, err := h.client.HeadBucket(bg, &s3.HeadBucketInput{Bucket: aws.String(name)})
		return err
	}), 404, kindEmpty)

	assertAPIError(t, bad(func() error {
		_, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{Bucket: aws.String(name)})
		return err
	}), 404, "NoSuchBucket", map[string]string{"BucketName": name})
}
