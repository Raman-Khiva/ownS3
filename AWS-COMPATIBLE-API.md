# oStore v1 --- S3-Compatible API Specification

**Status:** MVP implementation specification\
**API style:** Amazon S3 REST API (path-style addressing)\
**S3 API reference version:** `2006-03-01`\
**Server:** Go `net/http`\
**Persistence:** PostgreSQL metadata + local filesystem object data

------------------------------------------------------------------------

## 1. Purpose and compatibility target

oStore exposes a subset of the Amazon S3 REST API so applications can
use an S3 client, such as AWS SDK for JavaScript v3, by configuring the
oStore endpoint.

The v1 target is the basic bucket and object lifecycle:

-   Create, list, inspect, and delete buckets.
-   Upload, download, inspect, and delete objects.
-   List objects using `ListObjectsV2`.
-   Return S3-style HTTP status codes, headers, XML success responses,
    and XML error responses.
-   Authenticate requests and authorize access to the authenticated
    account's buckets.

This is a **defined subset of S3**, not a claim of complete Amazon S3
compatibility. Unsupported operations must fail explicitly rather than
silently succeeding.

### Important SDK compatibility requirement

The normal AWS SDK for JavaScript v3 S3 client signs requests using
**AWS Signature Version 4 (SigV4)**. Therefore, standard SDK
interoperability requires oStore to verify SigV4 (or use a deliberately
configured alternative signer on the client).

If v1 only checks a fixed access key and secret using custom logic, it
may be useful for local development, but it is **not standard AWS SDK
authentication compatibility**. The project should make this distinction
explicit. For the intended "change the endpoint and use the AWS SDK"
experience, implement SigV4 verification in v1.

Official references:

-   [Amazon S3 API
    Reference](https://docs.aws.amazon.com/AmazonS3/latest/API/)
-   [Amazon S3 API
    operations](https://docs.aws.amazon.com/AmazonS3/latest/API/API_Operations.html)
-   [AWS SDK for JavaScript v3 --- S3
    client](https://docs.aws.amazon.com/AWSJavaScriptSDK/v3/latest/client/s3/)

------------------------------------------------------------------------

## 2. Endpoint and addressing

Development endpoint:

``` text
http://localhost:9000
```

Use **path-style addressing**:

``` text
http://localhost:9000/{bucket}/{key}
```

Examples:

``` text
GET    http://localhost:9000/
PUT    http://localhost:9000/photos
PUT    http://localhost:9000/photos/profile/avatar.png
GET    http://localhost:9000/photos/profile/avatar.png
```

Configure the JavaScript SDK with `forcePathStyle: true`:

``` typescript
import { S3Client } from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  forcePathStyle: true,
  credentials: {
    accessKeyId: process.env.OSTORE_ACCESS_KEY!,
    secretAccessKey: process.env.OSTORE_SECRET_KEY!,
  },
});
```

### Addressing rules

-   The first path segment identifies the bucket.
-   The remaining path identifies the complete object key.
-   Object keys may contain `/` and must not be treated as filesystem
    paths.
-   Preserve the logical object key; do not use it directly to construct
    a disk path.
-   V1 supports path-style addressing only. Virtual-hosted-style
    addressing (for example, `bucket.storage.example.com/key`) is out of
    scope.
-   Test URL-encoded characters, spaces, plus signs, percent signs,
    Unicode, repeated slashes, and keys ending in `/`. Do not casually
    normalize or clean object-key paths.

------------------------------------------------------------------------

## 3. Endpoint inventory

There are **9 S3 operations** represented by **9 method/path patterns**
in the initial router.

  -------------------------------------------------------------------------------------------
                 \# S3 operation      Method        Path                      Purpose
  ----------------- ----------------- ------------- ------------------------- ---------------
                  1 `ListBuckets`     `GET`         `/`                       List buckets
                                                                              visible to the
                                                                              authenticated
                                                                              account

                  2 `CreateBucket`    `PUT`         `/{bucket}`               Create a bucket

                  3 `HeadBucket`      `HEAD`        `/{bucket}`               Check bucket
                                                                              existence and
                                                                              access

                  4 `DeleteBucket`    `DELETE`      `/{bucket}`               Delete an empty
                                                                              bucket

                  5 `ListObjectsV2`   `GET`         `/{bucket}?list-type=2`   List objects in
                                                                              a bucket

                  6 `PutObject`       `PUT`         `/{bucket}/{key...}`      Upload or
                                                                              replace an
                                                                              object

                  7 `GetObject`       `GET`         `/{bucket}/{key...}`      Download an
                                                                              object

                  8 `HeadObject`      `HEAD`        `/{bucket}/{key...}`      Retrieve object
                                                                              metadata
                                                                              without its
                                                                              body

                  9 `DeleteObject`    `DELETE`      `/{bucket}/{key...}`      Delete an
                                                                              object
  -------------------------------------------------------------------------------------------

`ListObjectsV2` uses the same `GET /{bucket}` path as a bucket-level GET
operation; the `list-type=2` query parameter selects the operation. It
is not a separate path pattern.

------------------------------------------------------------------------

# 4. Bucket operations

## 4.1 ListBuckets

**Purpose:** Return the buckets that belong to, or are accessible by,
the authenticated account.

  Property          Value
  ----------------- ----------------------
  Method            `GET`
  Path              `/`
  SDK command       `ListBucketsCommand`
  Request body      None
  Success status    `200 OK`
  Response format   S3 XML

Example request:

``` http
GET / HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Processing

1.  Verify the request authentication.
2.  Resolve the authenticated access key to an internal
    account/principal.
3.  Query PostgreSQL for buckets visible to that principal.
4.  Return only those buckets in the S3 `ListAllMyBucketsResult` XML
    response.

Example query for an owner-based v1 model:

``` sql
SELECT id, name, created_at
FROM buckets
WHERE owner_id = $1
ORDER BY name;
```

Example response (illustrative):

``` xml
<?xml version="1.0" encoding="UTF-8"?>
<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Owner>
    <ID>account-id</ID>
    <DisplayName>account</DisplayName>
  </Owner>
  <Buckets>
    <Bucket>
      <Name>photos</Name>
      <CreationDate>2026-11-07T10:00:00.000Z</CreationDate>
    </Bucket>
  </Buckets>
</ListAllMyBucketsResult>
```

### Requirements

-   Never enumerate the filesystem to produce this response. PostgreSQL
    is the source of truth for bucket ownership and listing.
-   Do not accept an `owner_id` from the request. Derive the principal
    from authenticated credentials.
-   Bucket names should be unique across the oStore endpoint, not merely
    unique per user, because path-style requests identify a bucket by
    name.
-   If the account has no buckets, return an empty `<Buckets/>`
    collection.
-   Do not leak another account's bucket names.

------------------------------------------------------------------------

## 4.2 CreateBucket

**Purpose:** Create a new bucket owned by the authenticated account.

  -----------------------------------------------------------------------
  Property                            Value
  ----------------------------------- -----------------------------------
  Method                              `PUT`

  Path                                `/{bucket}`

  SDK command                         `CreateBucketCommand`

  Request body                        Usually empty; may contain
                                      `CreateBucketConfiguration` XML

  Success status                      `200 OK`

  Response headers                    `Location: /{bucket}`
  -----------------------------------------------------------------------

Example:

``` http
PUT /photos HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Processing

1.  Authenticate the request and obtain the account ID.
2.  Validate the bucket name.
3.  Check that the name is not already allocated.
4.  Generate an internal bucket UUID.
5.  Insert the bucket metadata into PostgreSQL.
6.  Create the bucket's physical directory structure.
7.  Return the successful S3 response.

Suggested metadata:

``` text
buckets
-------
id          UUID PRIMARY KEY
name        VARCHAR(63) UNIQUE NOT NULL
owner_id    UUID NOT NULL
created_at  TIMESTAMPTZ NOT NULL
```

Suggested filesystem layout:

``` text
/var/lib/ostore/
└── buckets/
    └── {bucket-uuid}/
        ├── objects/
        └── tmp/
```

### Requirements

-   Validate S3 bucket naming rules. Do not accept arbitrary filesystem
    names.
-   Use a generated UUID for the directory name; never use the bucket
    name as a path component.
-   A bucket name must be globally unique within this oStore endpoint.
-   Associate the bucket with the authenticated account.
-   A bucket initially contains no objects.
-   If accepting `CreateBucketConfiguration`, validate the requested
    location constraint against the region(s) oStore supports. V1 may
    support one configured region only.
-   Filesystem creation and a PostgreSQL transaction are not one atomic
    transaction. Define compensating cleanup for failures so a failed
    create does not leave an untracked directory or database row.

Example success response:

``` http
HTTP/1.1 200 OK
Location: /photos
```

------------------------------------------------------------------------

## 4.3 HeadBucket

**Purpose:** Check whether a bucket exists and whether the caller can
access it.

  Property                Value
  ----------------------- ---------------------
  Method                  `HEAD`
  Path                    `/{bucket}`
  SDK command             `HeadBucketCommand`
  Request body            None
  Success status          `200 OK`
  Success response body   Empty

Example:

``` http
HEAD /photos HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Processing

1.  Authenticate the caller.
2.  Look up the bucket by name.
3.  Verify the caller has access to it.
4.  Return an empty successful response.

### Requirements

-   Return `200 OK` for an accessible bucket.
-   Return an appropriate S3 error status for missing or inaccessible
    buckets.
-   Do not return an XML body for a successful HEAD request.
-   HEAD responses must not contain a response body.

------------------------------------------------------------------------

## 4.4 DeleteBucket

**Purpose:** Delete an empty bucket owned by the authenticated account.

  Property                Value
  ----------------------- -----------------------
  Method                  `DELETE`
  Path                    `/{bucket}`
  SDK command             `DeleteBucketCommand`
  Request body            None
  Success status          `204 No Content`
  Success response body   Empty

Example:

``` http
DELETE /photos HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Processing

1.  Authenticate and authorize the caller.
2.  Find the bucket.
3.  Check that it contains no objects.
4.  Remove the bucket's storage directory.
5.  Delete the bucket metadata.
6.  Return `204 No Content`.

### Requirements

-   Only the owner (or an explicitly authorized principal) may delete
    the bucket.
-   Reject deletion when the bucket contains objects.
-   Return `BucketNotEmpty` for a non-empty bucket.
-   Do not recursively delete object data as a side effect of
    `DeleteBucket`.
-   Handle filesystem/database failure and partial deletion safely.
-   Missing-bucket behavior should use a consistent S3-style
    `NoSuchBucket` response.

------------------------------------------------------------------------

# 5. Object operations

## 5.1 PutObject

**Purpose:** Upload a new object or replace an existing object with the
same key.

  Property                    Value
  --------------------------- ----------------------
  Method                      `PUT`
  Path                        `/{bucket}/{key...}`
  SDK command                 `PutObjectCommand`
  Request body                Raw object bytes
  Success status              `200 OK`
  Important response header   `ETag`

Example:

``` http
PUT /photos/profile/avatar.png HTTP/1.1
Host: localhost:9000
Content-Type: image/png
Content-Length: 12345
Authorization: AWS4-HMAC-SHA256 ...

<binary object data>
```

### Important request headers

  -----------------------------------------------------------------------
  Header                              V1 behavior
  ----------------------------------- -----------------------------------
  `Content-Length`                    Enforce configured maximum object
                                      size; support known-length bodies

  `Content-Type`                      Persist and return on GET/HEAD;
                                      default to
                                      `application/octet-stream`

  `Content-MD5`                       Validate when supplied if
                                      implemented in v1

  `Cache-Control`                     Persist and return

  `Content-Disposition`               Persist and return

  `Content-Encoding`                  Persist and return

  `Expires`                           Persist and return

  `x-amz-meta-*`                      Persist user metadata and return it
                                      on GET/HEAD

  `x-amz-content-sha256`              Used as part of SigV4 payload
                                      verification where applicable
  -----------------------------------------------------------------------

### Processing

1.  Authenticate and authorize access to the bucket.
2.  Validate bucket and object key.
3.  Generate an internal object UUID.
4.  Stream the request body into a temporary file. Do not read the
    entire object into RAM.
5.  Calculate the required checksum(s) while streaming.
6.  Flush and close the temporary file.
7.  Atomically rename it to its final physical location.
8.  Insert or update the PostgreSQL object metadata.
9.  Return the S3 response with an ETag.

Suggested object metadata:

``` text
objects
-------
id             UUID PRIMARY KEY
bucket_id      UUID NOT NULL
object_key     TEXT NOT NULL
storage_path   TEXT NOT NULL
size           BIGINT NOT NULL
content_type   TEXT
etag           TEXT
checksum       TEXT
created_at     TIMESTAMPTZ NOT NULL
updated_at     TIMESTAMPTZ NOT NULL

UNIQUE (bucket_id, object_key)
```

### Requirements

-   Stream bytes using Go's `io.Reader`/`io.Copy` rather than
    `io.ReadAll`.
-   Store physical files under generated IDs, not user-provided keys.
-   Replacing an object must not expose a partially written file.
-   Ensure old and new file cleanup is handled if metadata persistence
    fails.
-   Enforce maximum object size.
-   Clean up temporary files after failed uploads.
-   V1 supports single-request, single-part uploads only. Multipart
    upload is out of scope.
-   Define ETag behavior explicitly. For ordinary single-part,
    unencrypted uploads, an MD5-derived ETag is conventional, but ETag
    must not be treated as a universal content checksum. Keep the
    independent SHA-256 integrity checksum in metadata.
-   If the SDK uses a streaming/chunked signing mode, implement the
    corresponding SigV4 streaming verification or document that mode as
    unsupported. Do not silently accept an invalid signature.

Example response:

``` http
HTTP/1.1 200 OK
ETag: "example-etag"
```

------------------------------------------------------------------------

## 5.2 GetObject

**Purpose:** Download an object's bytes.

  Property         Value
  ---------------- -----------------------
  Method           `GET`
  Path             `/{bucket}/{key...}`
  SDK command      `GetObjectCommand`
  Request body     None
  Success status   `200 OK`
  Response body    Streamed object bytes

Example:

``` http
GET /photos/profile/avatar.png HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Processing

1.  Authenticate and authorize access to the bucket.
2.  Find the object metadata using `(bucket_id, object_key)`.
3.  Open the generated physical file.
4.  Set the response headers from metadata.
5.  Stream the file to `http.ResponseWriter`.
6.  Close the file.

### Response headers

  Header                  Source
  ----------------------- -------------------------------
  `Content-Length`        Stored object size
  `Content-Type`          Stored content type
  `ETag`                  Stored ETag
  `Last-Modified`         Object modification timestamp
  `Cache-Control`         Stored value, if present
  `Content-Disposition`   Stored value, if present
  `Content-Encoding`      Stored value, if present
  `Expires`               Stored value, if present
  `x-amz-meta-*`          Stored user metadata

### Requirements

-   Stream from disk; do not load the entire object into memory.
-   Return `NoSuchBucket` if the bucket does not exist.
-   Return `NoSuchKey` if the object does not exist.
-   Do not expose physical storage paths.
-   If metadata exists but the file is missing, log an integrity/storage
    error and return an S3-style server error.
-   V1 may defer byte-range requests (`Range` / `206 Partial Content`)
    and conditional GET behavior. If deferred, document and test the
    unsupported behavior explicitly.

------------------------------------------------------------------------

## 5.3 HeadObject

**Purpose:** Return object metadata without returning its bytes.

  Property                Value
  ----------------------- ----------------------
  Method                  `HEAD`
  Path                    `/{bucket}/{key...}`
  SDK command             `HeadObjectCommand`
  Request body            None
  Success status          `200 OK`
  Success response body   Empty

Example:

``` http
HEAD /photos/profile/avatar.png HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Processing

1.  Authenticate and authorize access.
2.  Query PostgreSQL for object metadata.
3.  Set the same relevant metadata headers used by `GetObject`.
4.  Return `200 OK` with no body.

### Requirements

-   Do not open or stream the complete object unless needed for an
    integrity check.
-   Return `NoSuchKey` when the object does not exist.
-   Return `NoSuchBucket` when the bucket does not exist.
-   A HEAD response must not include a response body.
-   Keep GET and HEAD metadata headers consistent.

------------------------------------------------------------------------

## 5.4 DeleteObject

**Purpose:** Delete an object.

  Property                Value
  ----------------------- -----------------------
  Method                  `DELETE`
  Path                    `/{bucket}/{key...}`
  SDK command             `DeleteObjectCommand`
  Request body            None
  Success status          `204 No Content`
  Success response body   Empty

Example:

``` http
DELETE /photos/profile/avatar.png HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Processing

1.  Authenticate and authorize access to the bucket.
2.  Find the object metadata.
3.  Remove the physical file.
4.  Delete the corresponding metadata row.
5.  Return `204 No Content`.

### Requirements

-   V1 has no object versioning or delete markers.
-   For an authorized request to a missing key in an existing bucket,
    return `204 No Content` to provide S3-style idempotent delete
    behavior.
-   Return `NoSuchBucket` if the bucket does not exist.
-   Ensure a failed file deletion does not leave metadata claiming the
    object was deleted.
-   Handle a missing physical file with an explicit
    consistency-repair/logging path.
-   Never allow a caller to delete an object in another account's
    bucket.

------------------------------------------------------------------------

# 6. ListObjectsV2

## 6.1 ListObjectsV2

**Purpose:** List objects in a bucket, with prefix filtering and
pagination.

  Property                   Value
  -------------------------- ------------------------
  Method                     `GET`
  Path                       `/{bucket}`
  Required query parameter   `list-type=2`
  SDK command                `ListObjectsV2Command`
  Request body               None
  Success status             `200 OK`
  Response format            S3 XML

Example:

``` http
GET /photos?list-type=2&prefix=profile/&max-keys=100 HTTP/1.1
Host: localhost:9000
Authorization: AWS4-HMAC-SHA256 ...
```

### Query parameters

  -----------------------------------------------------------------------
  Parameter               V1 requirement          Description
  ----------------------- ----------------------- -----------------------
  `list-type`             Required; value `2`     Selects ListObjectsV2

  `prefix`                Supported               Return keys beginning
                                                  with this string

  `max-keys`              Supported               Maximum number of
                                                  results; cap at 1000

  `continuation-token`    Supported               Opaque token for the
                                                  next page

  `delimiter`             Recommended             Groups keys into
                                                  `CommonPrefixes`,
                                                  commonly `/`

  `start-after`           Supported               Begin listing
                                                  lexicographically after
                                                  this key

  `encoding-type`         Optional                Support `url` encoding
                                                  if implemented

  `fetch-owner`           Optional/deferred       Object owner
                                                  information; can be
                                                  omitted if not
                                                  supported
  -----------------------------------------------------------------------

### Processing

1.  Authenticate and authorize access to the bucket.
2.  Validate query parameters.
3.  Query PostgreSQL for keys in the bucket matching the prefix.
4.  Apply delimiter grouping if requested.
5.  Apply lexicographic ordering and pagination.
6.  Return the S3 `ListBucketResult` XML response.

Suggested query shape (pagination details may require a cursor-aware
query):

``` sql
SELECT object_key, size, etag, updated_at, content_type
FROM objects
WHERE bucket_id = $1
  AND object_key LIKE $2
ORDER BY object_key
LIMIT $3;
```

Use a safe prefix strategy; escape SQL `LIKE` wildcard characters (`%`
and `_`) when treating the supplied prefix literally. Add a suitable
index, such as `(bucket_id, object_key)`, to support listing.

### Response fields

The XML response should include the applicable fields:

-   `Name`
-   `Prefix`
-   `Delimiter` (when supplied)
-   `MaxKeys`
-   `KeyCount`
-   `IsTruncated`
-   `ContinuationToken` (when supplied)
-   `NextContinuationToken` (when another page exists)
-   `StartAfter` (when supplied)
-   `Contents` entries with `Key`, `LastModified`, `ETag`, `Size`, and
    `StorageClass`
-   `CommonPrefixes` when delimiter grouping is used

Example response (illustrative):

``` xml
<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>photos</Name>
  <Prefix>profile/</Prefix>
  <MaxKeys>100</MaxKeys>
  <KeyCount>1</KeyCount>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>profile/avatar.png</Key>
    <LastModified>2026-11-07T10:00:00.000Z</LastModified>
    <ETag>"example-etag"</ETag>
    <Size>12345</Size>
    <StorageClass>STANDARD</StorageClass>
  </Contents>
</ListBucketResult>
```

### Pagination requirements

-   Results must be sorted lexicographically by object key.
-   `max-keys` must be validated and capped at 1000.
-   A continuation token must be opaque to clients and validated by the
    server.
-   The next page must not repeat the previous page's final key.
-   `IsTruncated` must accurately indicate whether more results exist.
-   Return `NextContinuationToken` only when another page exists.
-   Test empty buckets, exact page boundaries, prefixes, delimiters, and
    keys containing special characters.
-   A continuation token should encode a stable cursor (for example, the
    last returned key plus any required query context), preferably
    authenticated or otherwise tamper-resistant.

------------------------------------------------------------------------

# 7. Go `net/http` route registration

The following uses method-aware routing and path values available in Go
1.22+.

``` go
func SetupRoutes() *http.ServeMux {
    mux := http.NewServeMux()

    // Bucket operations
    mux.HandleFunc("GET /{$}", listBuckets)
    mux.HandleFunc("PUT /{bucket}", createBucket)
    mux.HandleFunc("HEAD /{bucket}", headBucket)
    mux.HandleFunc("DELETE /{bucket}", deleteBucket)
    mux.HandleFunc("GET /{bucket}", getBucket) // dispatch ListObjectsV2

    // Object operations
    mux.HandleFunc("PUT /{bucket}/{key...}", putObject)
    mux.HandleFunc("GET /{bucket}/{key...}", getObject)
    mux.HandleFunc("HEAD /{bucket}/{key...}", headObject)
    mux.HandleFunc("DELETE /{bucket}/{key...}", deleteObject)

    return mux
}
```

Bucket-level GET dispatch:

``` go
func getBucket(w http.ResponseWriter, r *http.Request) {
    if r.URL.Query().Get("list-type") == "2" {
        listObjectsV2(w, r)
        return
    }

    // ListObjects V1 is not part of this MVP.
    writeS3Error(w, http.StatusNotImplemented, "NotImplemented",
        "Only ListObjectsV2 is supported")
}
```

Extract route values:

``` go
bucket := r.PathValue("bucket")
key := r.PathValue("key")
```

### Routing cautions

-   Use `GET /{$}` for the exact root path. A plain `GET /` pattern can
    act as a catch-all subtree pattern in `ServeMux`.
-   The `GET /{bucket}` handler dispatches based on `list-type=2`; do
    not register a duplicate GET pattern for the same path.
-   `/{bucket}/{key...}` is a catch-all for the object key after the
    bucket segment.
-   Go's URL parsing and `ServeMux` path unescaping need deliberate
    tests for encoded keys. Preserve the S3 logical key semantics; do
    not apply filesystem path cleaning to keys.
-   Keep authentication and request-ID middleware outside the individual
    handlers.

------------------------------------------------------------------------

# 8. Authentication, authorization, and ownership

## Authentication

For normal AWS SDK interoperability, verify AWS Signature Version 4:

-   Parse the `Authorization` header.
-   Identify the access key.
-   Load or resolve the associated secret credential.
-   Reconstruct the canonical request.
-   Verify the signature and signed headers.
-   Validate the request timestamp and credential scope.
-   Verify the payload hash or the explicitly supported payload-signing
    mode.

Do not log secret keys, authorization headers, or full signatures.

## Authorization

Authentication answers **who sent the request**. Authorization answers
**whether that principal can perform the operation**.

For the initial owner-based model:

-   Resolve the access key to an internal account/principal.
-   Store `owner_id` on each bucket.
-   `ListBuckets` returns only that principal's buckets.
-   Every bucket and object operation checks ownership before accessing
    metadata or files.
-   Never accept the owner ID from a request parameter.
-   Bucket names should be unique across the entire oStore endpoint.

The filesystem can keep all bucket directories under one root:

``` text
/var/lib/ostore/buckets/{bucket-uuid}/
```

PostgreSQL, not directory enumeration, determines ownership and
visibility.

------------------------------------------------------------------------

# 9. Common response and error behavior

All errors should use a consistent S3-style XML structure, except where
the HTTP method (such as HEAD) requires an empty response body.

Illustrative error:

``` xml
<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>NoSuchBucket</Code>
  <Message>The specified bucket does not exist.</Message>
  <Resource>/photos</Resource>
  <RequestId>request-id</RequestId>
</Error>
```

Recommended initial error mapping:

  ----------------------------------------------------------------------------
  Condition                              HTTP status S3 error code
  --------------------- ---------------------------- -------------------------
  Invalid bucket name                            400 `InvalidBucketName`

  Invalid                                        400 `InvalidRequest` or
  query/header/body                                  `InvalidArgument`

  Invalid or missing                             403 `AccessDenied` or
  credentials                                        appropriate
                                                     authentication error

  Signature mismatch                             403 `SignatureDoesNotMatch`

  Authenticated                                  403 `AccessDenied`
  principal lacks                                    
  access                                             

  Bucket not found                               404 `NoSuchBucket`

  Object not found on                            404 `NoSuchKey`
  GET/HEAD                                           

  Bucket name already                            409 `BucketAlreadyExists`
  allocated                                          

  Bucket contains                                409 `BucketNotEmpty`
  objects                                            

  Request body exceeds                           413 `EntityTooLarge`
  configured limit                                   

  Unsupported S3                                 501 `NotImplemented`
  operation or feature                               

  Unexpected                                     500 `InternalError`
  storage/database                                   
  failure                                            
  ----------------------------------------------------------------------------

Additional rules:

-   Return the appropriate S3 XML content type for XML responses.
-   Include a request ID in responses and logs.
-   Do not expose SQL errors, local paths, stack traces, or secrets to
    clients.
-   Successful `HEAD` operations have no response body.
-   Successful `DeleteObject` and `DeleteBucket` responses have no
    response body.
-   `DeleteObject` on a missing key in an existing bucket is idempotent
    and returns `204` in this unversioned MVP.

------------------------------------------------------------------------

# 10. Object metadata and filesystem contract

PostgreSQL stores logical metadata. The filesystem stores object bytes.

Example layout:

``` text
/var/lib/ostore/
└── buckets/
    └── {bucket-uuid}/
        ├── objects/
        │   ├── {object-uuid}
        │   └── {object-uuid}
        └── tmp/
            └── {upload-id}
```

Rules:

-   Bucket directory names are generated bucket UUIDs.
-   Object file names are generated object UUIDs.
-   Object keys are stored in PostgreSQL and are never used directly as
    disk paths.
-   Uploads are written to temporary files and finalized by rename.
-   The metadata row maps `(bucket_id, object_key)` to a generated
    storage path.
-   Define recovery/cleanup behavior for a crash between file
    finalization and metadata commit.
-   Do not let S3 handlers manipulate files or query PostgreSQL
    directly. They call services, which call the metadata repository and
    storage engine.

------------------------------------------------------------------------

# 11. Explicitly out of scope for v1

The following operations are not included in the initial nine-operation
MVP:

-   Multipart uploads: `CreateMultipartUpload`, `UploadPart`,
    `CompleteMultipartUpload`, `AbortMultipartUpload`, `ListParts`,
    `ListMultipartUploads`.
-   Multi-object delete: `DeleteObjects`.
-   Object copy: `CopyObject`.
-   Presigned URL query authentication.
-   Object versioning and delete markers.
-   Bucket ACLs and object ACLs.
-   Bucket policies and IAM policy evaluation.
-   Lifecycle policies.
-   Replication.
-   Notifications.
-   CORS configuration.
-   Object tagging.
-   Object lock and retention.
-   Website hosting.
-   SelectObjectContent.
-   Virtual-hosted-style addressing.
-   Range GET and conditional request behavior (unless added
    deliberately during implementation).

When an unsupported operation is requested, return a clear S3-style
`NotImplemented` response. Do not return success while ignoring the
requested feature.

------------------------------------------------------------------------

# 12. AWS SDK for JavaScript v3 acceptance tests

Use the official SDK to test the supported operations against the local
endpoint.

``` typescript
import {
  S3Client,
  CreateBucketCommand,
  ListBucketsCommand,
  HeadBucketCommand,
  PutObjectCommand,
  GetObjectCommand,
  HeadObjectCommand,
  ListObjectsV2Command,
  DeleteObjectCommand,
  DeleteBucketCommand,
} from "@aws-sdk/client-s3";

const s3 = new S3Client({
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  forcePathStyle: true,
  credentials: {
    accessKeyId: process.env.OSTORE_ACCESS_KEY!,
    secretAccessKey: process.env.OSTORE_SECRET_KEY!,
  },
});

const bucket = "ostore-integration-test";

await s3.send(new CreateBucketCommand({ Bucket: bucket }));

await s3.send(new HeadBucketCommand({ Bucket: bucket }));

await s3.send(new PutObjectCommand({
  Bucket: bucket,
  Key: "folder/hello.txt",
  Body: "Hello from oStore",
  ContentType: "text/plain",
}));

const head = await s3.send(new HeadObjectCommand({
  Bucket: bucket,
  Key: "folder/hello.txt",
}));

const list = await s3.send(new ListObjectsV2Command({
  Bucket: bucket,
  Prefix: "folder/",
}));

const object = await s3.send(new GetObjectCommand({
  Bucket: bucket,
  Key: "folder/hello.txt",
}));

const body = await object.Body?.transformToString();
if (body !== "Hello from oStore") {
  throw new Error("GetObject returned unexpected content");
}

await s3.send(new DeleteObjectCommand({
  Bucket: bucket,
  Key: "folder/hello.txt",
}));

await s3.send(new DeleteBucketCommand({ Bucket: bucket }));

console.log({ head, list, body });
```

This is an acceptance-test outline. It assumes SigV4 verification is
implemented and that the SDK's chosen request signing/payload mode is
supported by oStore.

### Required test cases

-   All nine supported operations succeed.
-   A user cannot list or access another user's buckets.
-   Duplicate bucket creation fails correctly.
-   Deleting a non-empty bucket fails.
-   GET/HEAD of a missing object returns the expected error.
-   Deleting a missing object is idempotent.
-   Upload replacement does not expose partial content.
-   Large object upload/download does not require loading the entire
    object into memory.
-   Prefix and continuation-token pagination return correct,
    non-duplicated results.
-   Special characters and nested object keys are preserved.
-   Invalid signatures and unauthorized access are rejected.
-   `go test -race ./...` passes.

------------------------------------------------------------------------

## 13. Definition of API v1 complete

The S3 API v1 is ready when:

-   [ ] All nine operations are implemented.
-   [ ] Standard AWS SDK v3 requests authenticate using the supported
    signing mode.
-   [ ] Request and response formats match the documented S3 behavior
    for the supported subset.
-   [ ] Bucket ownership is enforced for every operation.
-   [ ] Object data is streamed to and from the filesystem.
-   [ ] Metadata and filesystem consistency failures are handled.
-   [ ] Pagination, prefixes, and delimiters are tested.
-   [ ] Unsupported operations return explicit errors.
-   [ ] Integration tests pass against the AWS SDK v3.

**V1 target:** A single-node, unversioned, path-style S3-compatible
object storage service with basic bucket and object operations, backed
by PostgreSQL and the local filesystem.
