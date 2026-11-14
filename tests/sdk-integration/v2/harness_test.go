// AWS SDK compatibility suite, v2: MinIO-lite contract for smaller projects.
//
// Passing this suite means OwnS3 is a reliable local S3 for typical app
// workflows (not a full AWS S3 clone). Builds on v1's nine core routes.
//
// Covered:
//
//	ListObjectsV2   prefix, delimiter, max-keys, continuation-token, start-after
//	CopyObject      same-bucket and cross-bucket
//	DeleteObjects   multi-key delete
//	Multipart       Create / UploadPart / Complete / Abort / ListParts
//	Range GET       206 Partial Content
//	User metadata   x-amz-meta-* round-trip
//	SigV4           valid accepted, bad/missing rejected (AccessDenied)
//	Presigned URLs  GET and PUT
//	Checksums       stock SDK checksum calculation (WhenSupported)
//	Reliability     atomic overwrite (never serve a torn object)
//
// Still out of scope (must return NotImplemented, not silent success):
// versioning, ACLs/policies, lifecycle, tagging, notifications, Select,
// object lock, website hosting, virtual-host addressing.
//
// Run:   S3_ENDPOINT=http://localhost:9000 go test -v -count=1 ./v2/
//
// Server must accept credentials below (or S3_ACCESS_KEY / S3_SECRET_KEY).
package v2

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

var (
	testsPassed atomic.Int64
	testsFailed atomic.Int64
)

func colorTestResult(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			testsFailed.Add(1)
			fmt.Fprint(os.Stdout, colorReset)
			return
		}
		testsPassed.Add(1)
		fmt.Fprintf(os.Stdout, "%s✓ PASS %s\n", colorGreenBold, t.Name())
	})
}

func TestMain(m *testing.M) {
	start := time.Now()
	code := m.Run()
	elapsed := time.Since(start).Round(time.Millisecond)

	passed := testsPassed.Load()
	failed := testsFailed.Load()
	total := passed + failed

	fmt.Fprint(os.Stdout, colorReset)
	fmt.Println()
	if failed == 0 {
		fmt.Printf("%s── stats ──────────────────────────────────────────%s\n", colorGreenBold, colorReset)
		fmt.Printf("%s  ✓ %d passed  ·  %d total  ·  %s%s\n", colorGreenBold, passed, total, elapsed, colorReset)
	} else {
		fmt.Printf("%s── stats ──────────────────────────────────────────%s\n", colorRedBold, colorReset)
		fmt.Printf("%s  ✗ %d failed  ·  %d passed  ·  %d total  ·  %s%s\n",
			colorRedBold, failed, passed, total, elapsed, colorReset)
	}
	os.Exit(code)
}

var bg = context.Background()

const (
	s3NS    = "http://s3.amazonaws.com/doc/2006-03-01/"
	xmlDecl = `<?xml version="1.0" encoding="UTF-8"?>`

	defaultAccessKey = "test-access-key"
	defaultSecretKey = "test-secret-key"
)

var (
	isoRe           = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	etagRe          = regexp.MustCompile(`^"[0-9a-f]{32}"$`)
	multipartETagRe = regexp.MustCompile(`^"[0-9a-f]{32}-\d+"$`)
)

func accessKey() string {
	if v := os.Getenv("S3_ACCESS_KEY"); v != "" {
		return v
	}
	if v := os.Getenv("OSTORE_ACCESS_KEY"); v != "" {
		return v
	}
	return defaultAccessKey
}

func secretKey() string {
	if v := os.Getenv("S3_SECRET_KEY"); v != "" {
		return v
	}
	if v := os.Getenv("OSTORE_SECRET_KEY"); v != "" {
		return v
	}
	return defaultSecretKey
}

func endpoint() string {
	if v := os.Getenv("S3_ENDPOINT"); v != "" {
		return v
	}
	return "http://localhost:9000"
}

// =====================================================================
// HTTP recorder
// =====================================================================

type exchange struct {
	Method string
	Path   string
	Query  string
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
		Query:  req.URL.RawQuery,
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

func (r *recorder) one(t *testing.T) exchange {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.log) != 1 {
		var seen []string
		for _, e := range r.log {
			seen = append(seen, fmt.Sprintf("%s %s?%s -> %d", e.Method, e.Path, e.Query, e.Status))
		}
		failf(t, "expected exactly 1 HTTP request, got %d: %v", len(r.log), seen)
	}
	return r.log[0]
}

func (r *recorder) last(t *testing.T) exchange {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.log) == 0 {
		fail(t, "expected at least 1 HTTP request, got 0")
	}
	return r.log[len(r.log)-1]
}

// =====================================================================
// Harness
// =====================================================================

type harnessOpts struct {
	accessKey string
	secretKey string
	checksums aws.RequestChecksumCalculation
}

type harness struct {
	client  *s3.Client
	presign *s3.PresignClient
	rec     *recorder
	base    string
	access  string
	secret  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, harnessOpts{})
}

func newHarnessWith(t *testing.T, opts harnessOpts) *harness {
	t.Helper()
	ak := opts.accessKey
	sk := opts.secretKey
	if ak == "" {
		ak = accessKey()
	}
	if sk == "" {
		sk = secretKey()
	}
	checksums := opts.checksums
	if checksums == 0 {
		checksums = aws.RequestChecksumCalculationWhenRequired
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableCompression = true
	rec := &recorder{base: tr}

	cfg, err := config.LoadDefaultConfig(bg,
		config.WithRegion("us-east-1"),
		config.WithHTTPClient(&http.Client{Transport: rec}),
		config.WithRetryer(func() aws.Retryer { return aws.NopRetryer{} }),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(ak, sk, ""),
		),
	)
	if err != nil {
		failf(t, "LoadDefaultConfig: %v", err)
	}

	ep := endpoint()
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ep)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = checksums
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &harness{
		client:  client,
		presign: s3.NewPresignClient(client),
		rec:     rec,
		base:    ep,
		access:  ak,
		secret:  sk,
	}
}

var (
	bucketSeq atomic.Int64
	runID     = strconv.FormatInt(time.Now().Unix(), 36)
)

func uniqueBucket() string {
	return fmt.Sprintf("sdkv2-%s-%d", runID, bucketSeq.Add(1))
}

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
	// Abort leftover multipart uploads so DeleteBucket can succeed.
	if list, err := h.client.ListMultipartUploads(bg, &s3.ListMultipartUploadsInput{
		Bucket: aws.String(bucket),
	}); err == nil {
		for _, u := range list.Uploads {
			h.client.AbortMultipartUpload(bg, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      u.Key,
				UploadId: u.UploadId,
			})
		}
	}

	var token *string
	for {
		list, err := h.client.ListObjectsV2(bg, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: token,
		})
		if err != nil {
			break
		}
		if len(list.Contents) > 0 {
			objs := make([]types.ObjectIdentifier, 0, len(list.Contents))
			for _, o := range list.Contents {
				objs = append(objs, types.ObjectIdentifier{Key: o.Key})
			}
			h.client.DeleteObjects(bg, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket),
				Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
			})
		}
		if !aws.ToBool(list.IsTruncated) {
			break
		}
		token = list.NextContinuationToken
	}
	h.client.DeleteBucket(bg, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
}

func (h *harness) put(t *testing.T, bucket, key string, body []byte, contentType string) string {
	t.Helper()
	return h.putMeta(t, bucket, key, body, contentType, nil)
}

func (h *harness) putMeta(t *testing.T, bucket, key string, body []byte, contentType string, meta map[string]string) string {
	t.Helper()
	in := &s3.PutObjectInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		Body:     bytes.NewReader(body),
		Metadata: meta,
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

func (h *harness) ok(t *testing.T, op func() error) exchange {
	t.Helper()
	h.rec.reset()
	if err := op(); err != nil {
		failf(t, "operation failed: %v", err)
	}
	return h.rec.one(t)
}

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

// unsignedGET hits a URL without AWS signing (presigned-URL consumer).
func unsignedGET(t *testing.T, url string, extraHeader http.Header) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fail(t, err)
	}
	for k, vs := range extraHeader {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		failf(t, "unsigned GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Clone(), body
}

func unsignedPUT(t *testing.T, url string, body []byte, contentType string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		fail(t, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		failf(t, "unsigned PUT: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Clone(), respBody
}

// =====================================================================
// XML / wire helpers
// =====================================================================

type anyEl struct{ XMLName xml.Name }

type errorXML struct {
	XMLName    xml.Name `xml:"Error"`
	Code       *string  `xml:"Code"`
	Message    *string  `xml:"Message"`
	BucketName *string  `xml:"BucketName"`
	Key        *string  `xml:"Key"`
	RequestID  *string  `xml:"RequestId"`
	HostID     *string  `xml:"HostId"`
	Resource   *string  `xml:"Resource"`
	Extra      []anyEl  `xml:",any"`
}

type commonPrefixXML struct {
	Prefix *string `xml:"Prefix"`
	Extra  []anyEl `xml:",any"`
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
	XMLName               xml.Name          `xml:"ListBucketResult"`
	Name                  *string           `xml:"Name"`
	Prefix                *string           `xml:"Prefix"`
	Delimiter             *string           `xml:"Delimiter"`
	MaxKeys               *int              `xml:"MaxKeys"`
	KeyCount              *int              `xml:"KeyCount"`
	IsTruncated           *bool             `xml:"IsTruncated"`
	EncodingType          *string           `xml:"EncodingType"`
	ContinuationToken     *string           `xml:"ContinuationToken"`
	NextContinuationToken *string           `xml:"NextContinuationToken"`
	StartAfter            *string           `xml:"StartAfter"`
	Contents              []objectXML       `xml:"Contents"`
	CommonPrefixes        []commonPrefixXML `xml:"CommonPrefixes"`
	Extra                 []anyEl           `xml:",any"`
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

type bodyKind int

const (
	kindXML bodyKind = iota
	kindEmpty
	kindRaw
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
}

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

func assertObjectHeaders(t *testing.T, ex exchange, size int, etag, ctype string) {
	t.Helper()
	h := ex.Header
	if got := h.Get("ETag"); got != etag {
		failf(t, "ETag %q, want %q", got, etag)
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
}

func metaHeader(key string) string {
	return "X-Amz-Meta-" + strings.ToLower(key)
}
