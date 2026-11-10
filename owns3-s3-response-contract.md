# OwnS3 --- S3-Compatible HTTP Response Contract

**Status:** First-version response specification\
**Scope:** S3 REST API operations implemented by OwnS3\
**Goal:** Return response status codes, headers, and XML bodies in the
shapes expected by S3-compatible clients and AWS SDKs.

> This document describes the S3 REST API response contract, not a
> custom JSON API. S3 REST responses are operation-specific: some return
> empty bodies, some return XML, and `GetObject` returns the raw object
> bytes. Do not wrap every response in a common JSON envelope.

------------------------------------------------------------------------

## 1. Global response conventions

### 1.1 Common headers

Use these headers consistently where applicable:

  -----------------------------------------------------------------------
  Header                  Purpose                 OwnS3 rule
  ----------------------- ----------------------- -----------------------
  `Content-Type`          Describes response body `application/xml` for
                          format                  XML; stored object MIME
                                                  type for `GetObject`

  `Content-Length`        Response body length in Set accurately when
                          bytes                   known; zero for empty
                                                  responses

  `ETag`                  Entity tag for an       Return for successful
                          object                  `PutObject` and
                                                  `GetObject`; quote the
                                                  value

  `Last-Modified`         Object modification     Return for `GetObject`
                          time                    and `HeadObject`

  `x-amz-request-id`      Identifies one request  Generate a unique ID
                                                  for every request and
                                                  return it in headers
                                                  and errors

  `Date`                  Response timestamp      Let Go's HTTP server
                                                  manage it unless there
                                                  is a reason to override

  `x-amz-version-id`      Object version          Omit until versioning
                          identifier              is implemented
  -----------------------------------------------------------------------

AWS also returns `x-amz-id-2` (`HostId`) in many responses. It is an
additional diagnostic identifier. OwnS3 can omit it in the first version
unless targeting clients that require it.

### 1.2 Content types

-   XML control-plane responses: `application/xml`
-   Object downloads: the object's stored `Content-Type`, or
    `application/octet-stream` if unknown
-   Empty responses: no body; do not invent a JSON body
-   Error responses: `application/xml`

### 1.3 Success responses are not all the same

  Operation             Success status Response body
  ----------------- ------------------ ------------------
  `CreateBucket`              `200 OK` Empty
  `DeleteBucket`      `204 No Content` Empty
  `HeadBucket`                `200 OK` Empty
  `ListBuckets`               `200 OK` XML
  `ListObjectsV2`             `200 OK` XML
  `PutObject`                 `200 OK` Empty
  `GetObject`                 `200 OK` Raw object bytes
  `HeadObject`                `200 OK` Empty
  `DeleteObject`      `204 No Content` Empty

A `HEAD` response must not contain a response body. The headers should
describe the resource as they would for the corresponding `GET`.

------------------------------------------------------------------------

## 2. Standard S3 error response

For S3 REST API errors, return an appropriate HTTP error status and an
XML body with this shape:

``` xml
<?xml version="1.0" encoding="UTF-8"?>
<Error>
    <Code>NoSuchKey</Code>
    <Message>The specified key does not exist.</Message>
    <Resource>/my-bucket/path/to/object.png</Resource>
    <RequestId>req-01HXYZ...</RequestId>
</Error>
```

### Error fields

  Element       Meaning
  ------------- --------------------------------------------------------
  `Code`        Machine-readable error identifier
  `Message`     Human-readable explanation
  `Resource`    Bucket/object path involved in the request
  `RequestId`   Same request identifier returned in `x-amz-request-id`

Set:

``` http
Content-Type: application/xml
x-amz-request-id: req-01HXYZ...
```

The HTTP status code is authoritative. Clients should not have to parse
the XML to know whether the request succeeded.

### Initial error-code mapping

  ------------------------------------------------------------------------------
  Condition                              HTTP status S3 error code
  --------------------- ---------------------------- ---------------------------
  Bucket does not exist                        `404` `NoSuchBucket`

  Object does not exist                        `404` `NoSuchKey`

  Bucket already exists  `409` or operation-specific `BucketAlreadyOwnedByYou`
  and is owned by             compatibility behavior 
  caller                                             

  Bucket name invalid                          `400` `InvalidBucketName`

  Invalid                                      `400` `InvalidRequest`
  request/path/key                                   

  Request body exceeds                         `413` `EntityTooLarge`
  configured limit                                   

  Content checksum                             `400` `BadDigest`
  mismatch                                           

  Bucket is not empty                          `409` `BucketNotEmpty`

  Unsupported                                  `501` `NotImplemented`
  operation/feature                                  

  Unexpected internal                          `500` `InternalError`
  error                                              

  Access denied (once                          `403` `AccessDenied`
  auth is implemented)                               
  ------------------------------------------------------------------------------

Do not expose SQL errors, filesystem paths, credentials, or stack traces
in `Message`. Log the detailed error server-side and return a safe
message to the client.

### Example: object not found

``` http
HTTP/1.1 404 Not Found
Content-Type: application/xml
x-amz-request-id: req-123
```

``` xml
<?xml version="1.0" encoding="UTF-8"?>
<Error>
    <Code>NoSuchKey</Code>
    <Message>The specified key does not exist.</Message>
    <Resource>/my-bucket/missing.png</Resource>
    <RequestId>req-123</RequestId>
</Error>
```

------------------------------------------------------------------------

## 3. Operation response contracts

## 3.1 CreateBucket

**Request:** `PUT /{bucket}`

**Success:** `200 OK`, empty body.

Recommended headers:

``` http
HTTP/1.1 200 OK
Location: /my-bucket
Content-Length: 0
x-amz-request-id: req-123
```

`Location` should contain the created bucket path. AWS's CreateBucket
REST API uses `200 OK` and a `Location` response header.

Errors to support initially:

-   Invalid bucket name → `400 InvalidBucketName`
-   Name already taken / bucket already exists → use the appropriate
    S3-compatible conflict behavior
-   Database or filesystem failure → `500 InternalError`

Do not return a custom plain-text message such as
`bucket created successfully` if AWS SDK compatibility is the target.

## 3.2 DeleteBucket

**Request:** `DELETE /{bucket}`

**Success:** `204 No Content`, empty body.

``` http
HTTP/1.1 204 No Content
x-amz-request-id: req-123
```

If the bucket does not exist, return `404 NoSuchBucket`. If it contains
objects, return `409 BucketNotEmpty`. Do not delete a non-empty bucket
in the first version.

## 3.3 HeadBucket

**Request:** `HEAD /{bucket}`

**Success:** `200 OK`, empty body.

``` http
HTTP/1.1 200 OK
x-amz-request-id: req-123
```

For a missing bucket, return an appropriate `404` response. AWS
HeadBucket errors generally have no response body; clients use the HTTP
status and headers. To maximize compatibility, consider returning an
empty body for HeadBucket errors too, rather than the standard XML error
body used by other operations.

## 3.4 ListBuckets

**Request:** `GET /`

**Success:** `200 OK`, XML body.

``` http
HTTP/1.1 200 OK
Content-Type: application/xml
x-amz-request-id: req-123
```

``` xml
<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Owner>
        <ID>ownS3-owner</ID>
        <DisplayName>ownS3</DisplayName>
    </Owner>
    <Buckets>
        <Bucket>
            <Name>my-bucket</Name>
            <CreationDate>2026-11-10T10:00:00.000Z</CreationDate>
        </Bucket>
    </Buckets>
</ListAllMyBucketsResult>
```

Implementation notes:

-   Return the bucket collection under `Buckets`.
-   Each bucket has at least `Name` and `CreationDate`.
-   Use UTC timestamps in ISO 8601 format.
-   The `Owner` values are placeholders for OwnS3's owner model. Keep
    them stable for a given account.
-   Return valid XML even when there are no buckets; use an empty
    `<Buckets></Buckets>` element.

## 3.5 ListObjectsV2

**Request:** `GET /{bucket}?list-type=2`

**Success:** `200 OK`, XML body.

``` http
HTTP/1.1 200 OK
Content-Type: application/xml
x-amz-request-id: req-123
```

``` xml
<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Name>my-bucket</Name>
    <Prefix></Prefix>
    <KeyCount>2</KeyCount>
    <MaxKeys>1000</MaxKeys>
    <IsTruncated>false</IsTruncated>
    <Contents>
        <Key>profile/avatar.png</Key>
        <LastModified>2026-11-10T10:15:00.000Z</LastModified>
        <ETag>&quot;d41d8cd98f00b204e9800998ecf8427e&quot;</ETag>
        <Size>1024</Size>
        <StorageClass>STANDARD</StorageClass>
    </Contents>
    <Contents>
        <Key>reports/annual.pdf</Key>
        <LastModified>2026-11-10T10:20:00.000Z</LastModified>
        <ETag>&quot;098f6bcd4621d373cade4e832627b4f6&quot;</ETag>
        <Size>2048</Size>
        <StorageClass>STANDARD</StorageClass>
    </Contents>
</ListBucketResult>
```

Required/important fields for the first version:

  -----------------------------------------------------------------------
  XML element                         Meaning
  ----------------------------------- -----------------------------------
  `Name`                              Bucket name

  `Prefix`                            Requested prefix (empty if omitted)

  `KeyCount`                          Number of returned entries

  `MaxKeys`                           Requested/effective page limit

  `IsTruncated`                       Whether more results remain

  `Contents`                          Repeated object entries

  `Key`                               Full object key

  `LastModified`                      Last modification time

  `ETag`                              Object entity tag

  `Size`                              Object size in bytes

  `StorageClass`                      Use `STANDARD` for the initial
                                      local-storage implementation
  -----------------------------------------------------------------------

For pagination, support `continuation-token` and return
`NextContinuationToken` when `IsTruncated` is `true`. Also plan for
`delimiter`, `CommonPrefixes`, `start-after`, and `encoding-type=url`.

Important: S3 keys are object names, not real directories.
`CommonPrefixes` is a listing presentation feature created by applying
`prefix` and `delimiter`; do not require physical directories to have
special S3 meaning.

## 3.6 PutObject

**Request:** `PUT /{bucket}/{key...}`

**Success:** `200 OK`, empty body, with `ETag`.

``` http
HTTP/1.1 200 OK
ETag: "d41d8cd98f00b204e9800998ecf8427e"
Content-Length: 0
x-amz-request-id: req-123
```

Rules:

-   Store the complete request body as the object bytes.
-   Preserve the request `Content-Type` as object metadata; use
    `application/octet-stream` if absent.
-   Return an ETag for the stored object.
-   For a simple single-part upload, an MD5-based ETag is a useful
    first-version convention. Do not assume ETag is always an MD5 in all
    S3 modes (for example, multipart uploads and some encryption
    configurations).
-   If the same `(bucket, key)` already exists, replace the object and
    update its metadata rather than creating a duplicate row.
-   Do not return the object bytes in the response.

## 3.7 GetObject

**Request:** `GET /{bucket}/{key...}`

**Success:** `200 OK`, raw object bytes (not XML and not JSON).

``` http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 102400
Last-Modified: Tue, 10 Nov 2026 10:15:00 GMT
ETag: "d41d8cd98f00b204e9800998ecf8427e"
Accept-Ranges: bytes
x-amz-request-id: req-123

<raw binary object bytes>
```

Return metadata headers from the stored object record. Use
`application/octet-stream` if the content type is unknown.

First-version scope can omit range requests, but do not advertise
`Accept-Ranges: bytes` until `Range` requests are actually implemented.
If range support is implemented later, return `206 Partial Content` and
the appropriate `Content-Range`.

## 3.8 HeadObject

**Request:** `HEAD /{bucket}/{key...}`

**Success:** `200 OK`, empty body.

Return the same object metadata headers as GetObject where applicable:

``` http
HTTP/1.1 200 OK
Content-Type: image/png
Content-Length: 102400
Last-Modified: Tue, 10 Nov 2026 10:15:00 GMT
ETag: "d41d8cd98f00b204e9800998ecf8427e"
x-amz-request-id: req-123
```

Do not stream or return the object bytes. If the object does not exist,
return `404` with an empty body for maximum HeadObject compatibility.

## 3.9 DeleteObject

**Request:** `DELETE /{bucket}/{key...}`

**Success:** `204 No Content`, empty body.

``` http
HTTP/1.1 204 No Content
x-amz-request-id: req-123
```

For the initial unversioned implementation, delete the filesystem object
and its metadata. AWS S3's unversioned DeleteObject operation returns
`204 No Content`. A delete of a key that does not exist is also
successful in S3's standard unversioned behavior; returning `204` is
therefore the compatibility-friendly choice.

------------------------------------------------------------------------

## 4. Go implementation pattern

Create one response helper layer rather than handcrafting error
responses in every handler.

Suggested files:

``` text
internal/
└── http/
    └── response/
        ├── errors.go
        ├── xml.go
        └── request_id.go
```

### 4.1 Error response type

``` go
package response

import "encoding/xml"

type S3Error struct {
    XMLName   xml.Name `xml:"Error"`
    Code      string   `xml:"Code"`
    Message   string   `xml:"Message"`
    Resource  string   `xml:"Resource,omitempty"`
    RequestID string   `xml:"RequestId"`
}
```

### 4.2 Write an S3 XML error

``` go
func WriteError(
    w http.ResponseWriter,
    status int,
    code string,
    message string,
    resource string,
    requestID string,
) {
    payload := S3Error{
        Code:      code,
        Message:   message,
        Resource:  resource,
        RequestID: requestID,
    }

    body, err := xml.MarshalIndent(payload, "", "    ")
    if err != nil {
        http.Error(w, "Internal server error", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/xml")
    w.Header().Set("x-amz-request-id", requestID)
    w.WriteHeader(status)

    _, _ = w.Write([]byte(xml.Header))
    _, _ = w.Write(body)
}
```

Imports:

``` go
import (
    "encoding/xml"
    "net/http"
)
```

The request ID should be generated once per request, ideally in
middleware, and reused by both the response header and error XML. Do not
generate a different ID inside each handler.

### 4.3 Empty success response

For an empty successful response, set headers first and then write the
status:

``` go
w.Header().Set("x-amz-request-id", requestID)
w.WriteHeader(http.StatusNoContent)
```

For `200 OK` with no body:

``` go
w.Header().Set("x-amz-request-id", requestID)
w.WriteHeader(http.StatusOK)
```

### 4.4 XML success response

Use Go's `encoding/xml` with response structs. Avoid manually
concatenating XML strings; the encoder handles XML escaping.

``` go
w.Header().Set("Content-Type", "application/xml")
w.Header().Set("x-amz-request-id", requestID)
w.WriteHeader(http.StatusOK)

if err := xml.NewEncoder(w).Encode(response); err != nil {
    // At this point headers/status may already have been sent.
    // Log the error; do not attempt to write a second HTTP response.
}
```

For XML responses that need an XML declaration, write `xml.Header`
before encoding, or marshal the struct first and write the declaration
plus bytes.

------------------------------------------------------------------------

## 5. Important HTTP handler rules

1.  **Write headers before the status/body.** Once `WriteHeader` or the
    first body write happens, headers are committed.
2.  **Never write a second response after one has started.** Log
    streaming/encoding failures after response commitment.
3.  **Do not use `log.Fatal` inside a handler.** It terminates the whole
    server process. Log the error and return an HTTP error.
4.  **Do not return plain text or JSON for S3 errors.** Use the S3 XML
    error shape (except operations such as HEAD where an empty body is
    expected).
5.  **Do not return a success status before the operation has
    succeeded.** For uploads, finish writing and committing the object
    before sending `200 OK`.
6.  **Use `r.Context()` for database calls.**
7.  **Escape XML values using `encoding/xml`.** Object keys and messages
    may contain characters that need XML escaping.
8.  **Do not assume ETag is always MD5.** Treat it as an opaque entity
    tag in the API contract.
9.  **Do not claim range support unless implemented.**
10. **Keep error details safe.** Never send internal SQL or filesystem
    errors to clients.

------------------------------------------------------------------------

## 6. First-version implementation checklist

-   [ ] Add request-ID middleware and `x-amz-request-id`.
-   [ ] Implement reusable XML error response helper.
-   [ ] Implement reusable empty success response helper.
-   [ ] Implement XML response structs for ListBuckets and
    ListObjectsV2.
-   [ ] Standardize status codes for all handlers.
-   [ ] Ensure all HEAD handlers return headers only, no body.
-   [ ] Ensure GetObject streams raw bytes with stored metadata headers.
-   [ ] Ensure PutObject returns an ETag and an empty body.
-   [ ] Ensure DeleteObject returns `204 No Content`.
-   [ ] Test XML parsing, not just whether the response looks readable
    in Postman.
-   [ ] Test with AWS SDK / S3 client operations after request signing
    is implemented.

------------------------------------------------------------------------

## 7. AWS references

The following AWS documentation defines the operation-specific response
contracts. Treat it as the source of truth when adding more S3
operations or optional headers.

-   [Amazon S3 REST error
    responses](https://docs.aws.amazon.com/AmazonS3/latest/developerguide/ErrorResponses.html)
-   [CreateBucket](https://docs.aws.amazon.com/AmazonS3/latest/API/API_CreateBucket.html)
-   [DeleteBucket](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucket.html)
-   [HeadBucket](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadBucket.html)
-   [ListBuckets](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListBuckets.html)
-   [ListObjectsV2](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html)
-   [PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html)
-   [GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)
-   [DeleteObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html)

**Compatibility note:** This is a practical first-version contract for
OwnS3's selected operations, not a claim of complete Amazon S3
compatibility. AWS SDK compatibility also depends on request parsing,
authentication/signature verification, XML namespace and serialization
details, query parameter behavior, and exact operation-specific error
semantics.
