# OwnS3 V2 --- S3 Compatibility and Reliability Specification

**Status:** Proposed\
**Version:** 2.0\
**Project:** OwnS3 --- Self-hosted, S3-compatible object storage\
**Primary goal:** Make ordinary applications, AWS SDKs, and common S3
tooling work against OwnS3 for everyday object-storage workflows.

------------------------------------------------------------------------

## 1. Purpose and scope

OwnS3 V1 provides a tested core CRUD surface: bucket operations, object
upload/download/deletion, metadata retrieval, and object listing. V2
closes the compatibility and reliability gaps that prevent
general-purpose applications from treating OwnS3 as a practical local S3
endpoint.

V2 prioritizes:

1.  Behaviors that common applications and SDKs depend on.
2.  Correct request and response semantics, not merely accepting
    requests.
3.  Explicit authentication behavior rather than silently accepting
    signed requests.
4.  Safe object writes and bounded resource consumption.
5.  Automated compatibility and regression tests.

V2 is not intended to reproduce every AWS S3 feature. Versioning,
lifecycle policies, replication, virtual-hosted bucket addressing, ACLs,
IAM-style policies, and clustering remain outside the initial V2 scope
unless later promoted by a concrete compatibility requirement.

## 2. V2 goals

At the end of V2, a developer should be able to:

-   Upload both small and large objects using common AWS SDK upload
    helpers and the AWS CLI.
-   List objects by prefix, group keys using a delimiter, and paginate
    results.
-   Copy objects within a bucket and between buckets if supported by the
    chosen API scope.
-   Delete multiple objects in one request.
-   Generate and use presigned URLs for object upload and download.
-   Use an explicitly configured open-local mode or authenticated SigV4
    mode.
-   Download complete objects or byte ranges.
-   Store and retrieve user-defined object metadata.
-   Overwrite objects without exposing partially written content.
-   Receive consistent S3-shaped errors.
-   Run OwnS3 with configured limits and predictable cleanup after
    failed uploads.

## 3. Compatibility principles

### 3.1 S3 semantics over superficial route coverage

An endpoint is not considered implemented merely because it returns a
success status. It must satisfy the relevant request parsing,
validation, response headers, response body, status codes, and state
transitions expected by S3-compatible clients.

### 3.2 Explicit security mode

OwnS3 must never silently ignore authentication headers while appearing
to support signed requests.

Supported modes:

-   **Open mode:** authentication is intentionally disabled for trusted
    local development. Requests are accepted without SigV4 verification.
-   **SigV4 mode:** requests must have a valid signature for a
    configured access key. Invalid, expired, malformed, or unauthorized
    requests fail with S3-compatible errors.

The active mode must be visible in configuration and startup logs. Open
mode must be documented as unsuitable for an untrusted network.

### 3.3 Compatibility testing

Compatibility must be verified with real clients, not only hand-written
HTTP requests. At minimum, the test matrix should include:

-   AWS SDK for Go v2.
-   AWS CLI.
-   A second commonly used SDK or application integration where
    practical.
-   OwnS3's own HTTP integration tests.

Client tests should cover both open mode and SigV4 mode where
applicable.

------------------------------------------------------------------------

## 4. Functional requirements

## 4.1 Multipart upload

**Priority:** P0 --- hard blocker

Multipart upload allows clients to upload large objects as independently
transferred parts and then assemble them into one object. The V1
database schema already contains multipart upload and part tables; V2
must connect that schema to a complete API and storage lifecycle.

### Required operations

  --------------------------------------------------------------------------------------------------
  Operation                 HTTP/API shape                                   Required behavior
  ------------------------- ------------------------------------------------ -----------------------
  CreateMultipartUpload     `POST /{bucket}/{key}?uploads`                   Create an upload
                                                                             session and return an
                                                                             `UploadId`.

  UploadPart                `PUT /{bucket}/{key}?partNumber=N&uploadId=ID`   Store or replace a
                                                                             numbered part and
                                                                             return its ETag.

  CompleteMultipartUpload   `POST /{bucket}/{key}?uploadId=ID`               Validate the supplied
                                                                             part list and assemble
                                                                             the final object.

  AbortMultipartUpload      `DELETE /{bucket}/{key}?uploadId=ID`             Abort the session and
                                                                             remove its temporary
                                                                             part data.

  ListParts                 `GET /{bucket}/{key}?uploadId=ID`                Return uploaded parts,
                                                                             with pagination if the
                                                                             implementation supports
                                                                             more than one response
                                                                             page.

  ListMultipartUploads      `GET /{bucket}?uploads`                          List active multipart
                                                                             sessions, with
                                                                             pagination.
  --------------------------------------------------------------------------------------------------

`ListParts` and `ListMultipartUploads` are included because they are
standard companion operations needed by clients and operators to inspect
and recover multipart state.

### Requirements

-   Generate a unique upload ID for every multipart session.
-   Persist bucket, key, upload ID, initiation time, and relevant
    metadata.
-   Validate that the bucket exists and the key is valid before creating
    an upload.
-   Validate upload ID, part number, and ownership relationship between
    the upload, bucket, and key.
-   Enforce S3-compatible part-number bounds: 1 through 10,000.
-   Support re-uploading a part number by replacing the previously
    stored part.
-   Return a stable ETag for every successfully uploaded part.
-   On completion, validate that requested parts exist and that the
    submitted part order is valid.
-   Assemble parts in the exact order supplied by the completion
    request.
-   Reject invalid or missing parts using an S3-shaped error response.
-   Ensure the final object is not visible in a partially assembled
    state.
-   On successful completion, persist the final object's size, ETag,
    content type, timestamps, and metadata.
-   Remove temporary part files and multipart database rows after
    successful completion.
-   Abort must be idempotent where practical and must not delete an
    already completed object.
-   Failed completion must not leave a corrupted final object.
-   Enforce configured limits for total object size, part size, and
    active uploads.
-   Add cleanup for abandoned multipart uploads. The cleanup policy must
    be configurable and documented.

### Acceptance criteria

-   A small multipart object can be uploaded and downloaded
    byte-for-byte.
-   A multi-part object can be completed with parts uploaded out of
    order, provided the completion manifest orders them correctly.
-   Re-uploading a part number replaces that part.
-   Completing with a missing part fails with an S3-compatible error.
-   Aborting removes temporary data and makes subsequent operations on
    that upload ID fail appropriately.
-   A completed upload leaves no active multipart session or temporary
    part files.
-   AWS SDK multipart upload helpers and AWS CLI large-file upload work
    in the compatibility test suite.

## 4.2 ListObjectsV2 correctness

**Priority:** P0 --- hard blocker

Implement `ListObjectsV2` as a real filtered and paginated listing API,
rather than returning a flat first page.

### Request parameters

  -----------------------------------------------------------------------
  Parameter                           Required behavior
  ----------------------------------- -----------------------------------
  `list-type=2`                       Select ListObjectsV2 behavior.

  `prefix`                            Return only keys beginning with the
                                      supplied prefix.

  `delimiter`                         Group keys after the prefix into
                                      `CommonPrefixes`.

  `max-keys`                          Limit the number of returned
                                      entries, within the supported S3
                                      maximum.

  `continuation-token`                Resume listing from the exact
                                      continuation position represented
                                      by the token.

  `start-after`                       Start listing lexicographically
                                      after the supplied key when no
                                      continuation token is used.

  `encoding-type`                     Support `url` encoding for
                                      key-related response values if
                                      required by tested clients.

  `fetch-owner`                       Return owner information only if
                                      OwnS3 has a defined owner model;
                                      otherwise document and test the
                                      chosen compatibility behavior.
  -----------------------------------------------------------------------

### Response requirements

Return the S3 XML response with:

-   `Name`
-   `Prefix`
-   `KeyCount`
-   `MaxKeys`
-   `IsTruncated`
-   `Contents` entries
-   `CommonPrefixes` entries when a delimiter is used
-   `ContinuationToken` when supplied
-   `NextContinuationToken` when more results remain
-   `StartAfter` when applicable
-   `EncodingType` when requested

Each `Contents` entry should include the fields supported by OwnS3's
object model, at minimum `Key`, `LastModified`, `ETag`, and `Size`.
Include `StorageClass` if required for client compatibility and use a
documented constant value.

### Correctness requirements

-   Sort keys lexicographically according to the chosen S3-compatible
    ordering.
-   Apply prefix filtering before pagination.
-   Apply delimiter grouping correctly. A key that contributes to a
    `CommonPrefixes` entry must not also be returned as an ordinary
    object entry for that grouped level.
-   Count returned objects and common prefixes consistently with S3
    semantics.
-   `max-keys=0` must be handled correctly.
-   Empty buckets and prefixes must return valid empty XML responses.
-   Continuation tokens must be opaque to clients and must not be
    treated as raw object keys.
-   Tokens must be validated. Invalid or malformed tokens must produce a
    consistent S3 error.
-   A continuation token must not cause duplicates or skipped entries
    across stable listings.
-   Define and document behavior when the bucket changes between pages.
    Strong snapshot consistency is not required for V2 unless
    implemented explicitly.

### Acceptance criteria

-   Prefix filtering returns only matching keys.
-   Delimiter listing returns the expected `CommonPrefixes`.
-   A listing larger than `max-keys` can be traversed page by page
    without duplicates or omissions when the dataset is unchanged.
-   `start-after` works independently of continuation tokens.
-   AWS SDK and CLI listing commands work with prefix and pagination
    options.

## 4.3 CopyObject

**Priority:** P0 --- hard blocker

Implement server-side object copying without requiring the client to
download and re-upload the object.

### Request behavior

-   Support `PUT /{destination-bucket}/{destination-key}` with
    `x-amz-copy-source`.
-   Parse and validate the source bucket and source key.
-   Correctly decode URL-encoded source paths and handle keys containing
    reserved characters.
-   Support copying within the same bucket and between buckets.
-   Validate source bucket and source object existence.
-   Validate destination bucket existence and destination key.
-   Reject unsupported source forms with an S3-compatible error.
-   Return the S3-compatible XML `CopyObjectResult`, including
    `LastModified` and `ETag`.

### Metadata behavior

Support `x-amz-metadata-directive`:

-   `COPY` (default): preserve source user metadata and relevant system
    metadata.
-   `REPLACE`: use metadata supplied on the copy request.

Define which system-controlled fields are regenerated, such as ETag and
last-modified time. Do not allow clients to overwrite server-controlled
values such as object size or storage path through user metadata.

### Conditional behavior

Implement commonly used source preconditions where practical:

-   `x-amz-copy-source-if-match`
-   `x-amz-copy-source-if-none-match`
-   `x-amz-copy-source-if-modified-since`
-   `x-amz-copy-source-if-unmodified-since`

If any are deferred, document the exact unsupported behavior and ensure
unsupported inputs do not silently produce incorrect results.

### Acceptance criteria

-   Copying an object creates an independent destination object with
    identical bytes.
-   Source and destination can be in the same bucket or different
    buckets.
-   `COPY` preserves user metadata.
-   `REPLACE` applies the supplied metadata.
-   Missing source and destination bucket produce correct S3-shaped
    errors.
-   Copying an object does not expose a partially written destination.

## 4.4 DeleteObjects --- multi-object delete

**Priority:** P0 --- hard blocker

Implement `POST /{bucket}?delete` for deleting multiple keys in one
request.

### Requirements

-   Parse the S3 XML `Delete` request body.
-   Support multiple `Object` entries and optional object version IDs
    only if versioning is later implemented; V2 should reject
    unsupported version-specific requests clearly.
-   Support the `Quiet` option.
-   Return an XML `DeleteResult`.
-   In verbose mode, return a `Deleted` entry for each requested key
    successfully handled.
-   Return per-object `Error` entries for failures where the S3 response
    model requires them.
-   Do not fail the entire request merely because one key is missing or
    one deletion fails.
-   Define behavior for duplicate keys in the same request.
-   Enforce a configurable maximum number of keys per request, with a
    maximum of 1,000 for S3 compatibility.
-   Validate XML and return a consistent S3 error for malformed bodies.
-   Remove object data and metadata safely. A missing key should follow
    S3's idempotent delete semantics.

### Acceptance criteria

-   One request can delete multiple existing objects.
-   Deleting a mixture of existing and missing keys returns a valid
    result.
-   Quiet mode omits successful `Deleted` entries while preserving
    errors.
-   AWS CLI recursive removal works against OwnS3.

## 4.5 Modern SDK upload compatibility

**Priority:** P0 --- hard blocker

Common SDKs may attach checksum headers, select checksum algorithms, or
use streaming payload modes. OwnS3 must support the common default
upload behavior rather than only a narrow hand-crafted PUT request.

### Required work

-   Build a compatibility matrix of checksum headers and payload modes
    emitted by supported SDK versions.
-   Support the checksum algorithms required by the chosen SDK
    compatibility baseline, prioritizing CRC32, CRC32C, SHA-1, and
    SHA-256 where emitted.
-   Validate supplied checksums when the request mode and algorithm are
    supported.
-   Return `BadDigest` when a supplied checksum does not match the
    received payload.
-   Return checksum response headers where the selected operation and
    algorithm require them.
-   Handle `Content-MD5` correctly when supplied.
-   Handle `x-amz-content-sha256` values used by supported SigV4 request
    modes.
-   Investigate and implement `aws-chunked` streaming payload decoding
    if required by the supported SDK defaults.
-   Correctly process relevant `x-amz-decoded-content-length` and
    chunk-signature framing when implementing aws-chunked.
-   Do not silently ignore a checksum header that the server claims to
    validate.
-   If a checksum mode is not supported, return a clear S3-compatible
    error rather than storing data with falsely implied integrity.

### Acceptance criteria

-   A standard AWS SDK upload using its normal checksum defaults
    succeeds.
-   A supported checksum mismatch returns `BadDigest` and does not
    publish the object.
-   `Content-MD5` is validated when supplied.
-   The AWS CLI and selected SDK compatibility tests pass without
    requiring undocumented client-side checksum workarounds.
-   Any unsupported streaming mode is documented with a reproducible
    failing test and a clear error response.

## 4.6 Presigned URLs

**Priority:** P1 --- high impact

Support presigned requests for browser uploads, downloads, and temporary
delegated access.

### Requirements

-   Implement SigV4 query-string authentication for presigned URLs.
-   Support presigned GET and PUT requests.
-   Validate credential scope, date, expiration, signed headers,
    canonical request, and signature.
-   Enforce the configured maximum expiration duration.
-   Reject expired URLs and tampered query parameters.
-   Ensure the HTTP method, path, query string, and signed headers are
    part of signature verification.
-   Do not log secret keys or full presigned URLs.
-   Work with the same credentials and signing configuration used by
    normal SigV4 requests.
-   CORS is a separate configuration concern; document that browser use
    may require CORS support/configuration.

### Acceptance criteria

-   A presigned GET URL downloads the intended object without an
    Authorization header.
-   A presigned PUT URL uploads the intended object without an
    Authorization header.
-   Expired URLs fail.
-   Changing the method, path, signed headers, or signed query
    parameters invalidates the signature.
-   A URL cannot be used to access a different object.

## 4.7 SigV4 authentication

**Priority:** P1 --- high impact

Move from ignored authentication headers to explicit open or verified
authentication.

### Configuration

Provide configuration for:

-   Authentication mode: `open` or `sigv4`.
-   Access key ID.
-   Secret access key.
-   Region or accepted signing region.
-   Service name (`s3`).
-   Optional clock-skew tolerance.

Credentials must be loaded from environment variables or a supported
secrets mechanism, not hard-coded in source code or committed
configuration.

### Requirements

-   Verify SigV4 Authorization headers for supported request types.
-   Verify canonical URI, canonical query string, canonical headers,
    signed headers, payload hash, credential scope, timestamp, and
    signature.
-   Compare signatures using a constant-time comparison.
-   Reject unknown access keys and invalid signatures.
-   Reject requests outside the allowed clock-skew window.
-   Support the payload-signing modes required by the compatibility
    matrix.
-   Return S3-compatible `AccessDenied` or authentication errors.
-   Ensure authentication runs before handlers perform protected object
    or bucket operations.
-   Keep open mode explicit and visibly configured.
-   Presigned URL verification must use the same signing primitives and
    credential configuration.

### Acceptance criteria

-   A correctly signed AWS SDK request succeeds in SigV4 mode.
-   An unsigned request fails in SigV4 mode.
-   An invalid signature fails.
-   An unknown access key fails.
-   A request signed for the wrong region or service fails unless that
    region/service is explicitly allowed.
-   Open mode continues to support local development without requiring
    credentials.

## 4.8 Range GET

**Priority:** P1 --- high impact

Implement byte-range retrieval for `GET Object`.

### Requirements

-   Parse the `Range` request header for supported byte ranges.
-   Support a single range in V2.
-   Return `206 Partial Content` for a satisfiable range.
-   Return `Content-Range` with the selected byte interval and total
    object size.
-   Return the correct `Content-Length`.
-   Return `Accept-Ranges: bytes` only when range behavior is
    implemented.
-   Return `416 Range Not Satisfiable` for an unsatisfiable range, with
    an appropriate `Content-Range: bytes */{size}` header.
-   Support common forms:
    -   `bytes=start-end`
    -   `bytes=start-`
    -   `bytes=-suffixLength`
-   Handle empty objects and malformed range headers safely.
-   Preserve normal GET behavior when no Range header is present.
-   Define and test behavior for multiple ranges. V2 may reject multiple
    ranges if multipart/byteranges is not implemented, but must do so
    predictably.

### Acceptance criteria

-   A range request returns exactly the requested bytes.
-   Open-ended and suffix ranges work.
-   Unsatisfiable ranges return 416.
-   Normal GET remains unchanged.
-   Large-object range reads do not load the entire object into memory.

## 4.9 User-defined object metadata

**Priority:** P1 --- high impact

Support `x-amz-meta-*` headers on object creation and copy operations.

### Requirements

-   Capture user metadata headers on PUT Object and supported multipart
    initiation/completion flows.
-   Persist metadata in the database or a suitable metadata
    representation.
-   Return user metadata headers on HEAD Object and GET Object.
-   Preserve metadata during CopyObject with the `COPY` directive.
-   Replace metadata during CopyObject with the `REPLACE` directive.
-   Normalize header names according to HTTP semantics while preserving
    metadata values.
-   Prevent user metadata from overwriting server-controlled response
    headers or internal fields.
-   Define limits for metadata count and total metadata size.
-   Handle invalid metadata values and header-size constraints
    consistently.

### Acceptance criteria

-   Uploading an object with `x-amz-meta-*` headers persists them.
-   HEAD and GET return the stored metadata.
-   Copy with `COPY` preserves metadata.
-   Copy with `REPLACE` changes metadata as requested.
-   Metadata survives server restart.

------------------------------------------------------------------------

## 5. Reliability and operational requirements

## 5.1 Atomic PutObject

**Priority:** P0 --- reliability blocker

The current direct-to-final-path write can expose a truncated or
partially overwritten object if a request fails during transfer. V2 must
make object publication atomic.

### Required write flow

1.  Validate bucket, key, headers, content length, and configured
    limits.
2.  Create a unique temporary file in the same filesystem as the final
    object path.
3.  Stream the request body into the temporary file.
4.  Calculate required integrity values while streaming.
5.  Verify content length and checksums.
6.  Flush and close the temporary file; sync it when durability settings
    require it.
7.  Atomically rename the temporary file to the final object path.
8.  Commit/update metadata in a controlled sequence.
9.  Clean up temporary files on all error paths.

### Consistency requirements

-   Readers must see either the previous complete object or the new
    complete object, never a partially written object.
-   A failed upload must not destroy a previously valid object.
-   Temporary files must be on the same filesystem as the destination to
    preserve atomic rename semantics.
-   Define recovery behavior for a crash between filesystem rename and
    database metadata commit.
-   Avoid path traversal and ensure all resolved paths remain under the
    configured storage root.
-   Apply the same safe-publication principle to CopyObject and
    multipart completion.

### Acceptance criteria

-   Interrupting an upload does not corrupt an existing object.
-   Concurrent readers never observe partial object contents.
-   Temporary files are cleaned up after ordinary failures.
-   Startup or maintenance recovery can identify and handle orphaned
    temporary files.
-   Tests cover failures before and after the rename/metadata boundary.

## 5.2 Object and request size limits

**Priority:** P0 --- reliability blocker

A shared local service must have bounded resource usage.

### Configuration

Provide documented settings for:

-   Maximum object size.
-   Maximum request body size.
-   Maximum multipart part size.
-   Maximum total multipart object size.
-   Maximum number of active multipart uploads.
-   Maximum number of parts per upload.
-   Maximum metadata size.
-   Maximum DeleteObjects keys per request.
-   Optional request timeout and idle connection timeout.

Defaults must be conservative enough for local development and clearly
documented. Limits must be configurable without source changes.

### Requirements

-   Enforce limits while streaming, not only after reading the full
    request.
-   Reject oversized requests with an appropriate S3 error and HTTP
    status.
-   Do not buffer large request bodies entirely in memory.
-   Clean up partial files and temporary multipart data after limit
    violations.
-   Ensure declared `Content-Length` is not blindly trusted; also
    enforce the actual bytes read.
-   Avoid integer overflow when calculating sizes and offsets.

### Acceptance criteria

-   Requests larger than configured limits are rejected predictably.
-   Rejected uploads leave no published object.
-   Memory usage remains bounded for large streamed uploads.
-   Multipart uploads cannot exceed the configured aggregate object
    limit.

## 5.3 Failed upload and orphan cleanup

**Priority:** P1 --- reliability

### Requirements

-   Remove temporary upload files when a request fails, is cancelled, or
    exceeds a limit.
-   Ensure request cancellation propagates to file-writing loops.
-   Track multipart initiation time and part activity.
-   Provide a configurable expiration policy for abandoned multipart
    sessions.
-   Implement cleanup on startup and/or periodic maintenance.
-   Cleanup must not remove files belonging to active uploads.
-   Log cleanup outcomes without exposing credentials or sensitive
    request data.
-   Provide a safe strategy for reconciling orphaned files and database
    rows after a crash.

### Acceptance criteria

-   Cancelled uploads do not leave unbounded temporary data.
-   Expired multipart sessions are eventually removed.
-   Cleanup is safe to run more than once.
-   Active multipart sessions are not deleted by cleanup.

## 5.4 Consistent S3 error responses

**Priority:** P0 --- reliability and SDK compatibility

All S3 API errors must use one shared response path. Handler-specific
plain-text errors such as `http.Error` must be removed from S3 routes.

### Requirements

-   Use a common error type containing S3 code, message, HTTP status,
    resource, and optional request ID.
-   Serialize errors using the S3 XML error format.
-   Set `Content-Type: application/xml` for XML error bodies.
-   Include request identifiers consistently where supported.
-   Ensure HEAD requests return the correct status and headers without a
    response body.
-   Ensure method-not-allowed responses use the correct status and S3
    error shape.
-   Map validation, missing bucket/key, access, checksum, range, and
    internal storage errors consistently.
-   Do not leak filesystem paths, SQL details, secret values, or stack
    traces to clients.
-   Log internal diagnostic details separately with a request ID for
    correlation.

### Minimum error cases to standardize

  ----------------------------------------------------------------------------
  Error code                              Typical status Example condition
  ------------------------- ---------------------------- ---------------------
  `NoSuchBucket`                                     404 Bucket does not
                                                         exist.

  `NoSuchKey`                                        404 Object does not
                                                         exist.

  `InvalidBucketName`                                400 Bucket name violates
                                                         constraints.

  `InvalidArgument`                                  400 Invalid or
                                                         unsupported parameter
                                                         value.

  `InvalidRequest`                                   400 Request shape or
                                                         combination is
                                                         invalid.

  `AccessDenied`                                     403 Authentication or
                                                         authorization fails.

  `SignatureDoesNotMatch`                            403 SigV4 signature is
                                                         invalid.

  `BadDigest`                                        400 Supplied checksum
                                                         does not match.

  `EntityTooLarge`                            400 or 413 Request exceeds
                                                         configured size
                                                         limit; use a
                                                         consistent documented
                                                         mapping.

  `InvalidRange`                                     416 Requested byte range
                                                         cannot be satisfied.

  `MethodNotAllowed`                                 405 HTTP method is not
                                                         supported for the
                                                         resource.

  `InternalError`                                    500 Unexpected
                                                         server-side failure.
  ----------------------------------------------------------------------------

### Acceptance criteria

-   No S3 route emits plain-text errors for normal API failures.
-   SDKs can parse errors from all implemented routes.
-   HEAD error responses have no body but retain the correct status and
    headers.
-   Internal implementation details are not exposed to clients.

------------------------------------------------------------------------

## 6. API and data-model work

The existing multipart tables should be reviewed before implementation.
Do not assume that the current schema covers all lifecycle and integrity
requirements.

### Database and metadata review

Review whether the schema supports:

-   Multipart upload ID uniqueness.
-   Bucket/key/upload relationships and foreign keys.
-   Unique part number per upload.
-   Part size, ETag, storage path, and timestamps.
-   Upload initiation time and cleanup state.
-   User metadata storage.
-   Atomic metadata publication and recovery markers.
-   Indexes for listing uploads, parts, and objects by
    bucket/key/prefix.

### Storage layout

Keep object bytes in the filesystem and object metadata in PostgreSQL,
consistent with the OwnS3 architecture.

Recommended separation:

-   Final object storage: immutable or atomically replaced object files.
-   Temporary PUT files: isolated temporary namespace.
-   Multipart part files: isolated by upload ID and part number.
-   Internal metadata: PostgreSQL records, not user-visible object keys.

The physical storage path must not be derived directly from an untrusted
key without safe path mapping. Object keys are logical identifiers;
storage paths should be generated or safely encoded by the storage
layer.

### Transaction boundaries

Document the ordering and recovery strategy for operations that touch
both PostgreSQL and the filesystem. PostgreSQL transactions cannot make
filesystem changes transactional. Implement explicit recovery behavior
for partial cross-resource failures.

------------------------------------------------------------------------

## 7. Configuration requirements

V2 configuration should be documented in one place, with defaults and
validation.

Suggested configuration surface:

  -----------------------------------------------------------------------
  Setting                             Purpose
  ----------------------------------- -----------------------------------
  `OWNS3_ADDR`                        Bind address and port.

  `OWNS3_STORAGE_ROOT`                Root directory for object and
                                      temporary data.

  `OWNS3_AUTH_MODE`                   `open` or `sigv4`.

  `OWNS3_ACCESS_KEY_ID`               Access key for SigV4 mode.

  `OWNS3_SECRET_ACCESS_KEY`           Secret key for SigV4 mode.

  `OWNS3_REGION`                      Accepted signing region.

  `OWNS3_MAX_OBJECT_SIZE`             Maximum final object size.

  `OWNS3_MAX_REQUEST_SIZE`            Maximum request body size.

  `OWNS3_MAX_PART_SIZE`               Maximum individual multipart part
                                      size.

  `OWNS3_MAX_ACTIVE_UPLOADS`          Maximum active multipart sessions.

  `OWNS3_MULTIPART_EXPIRY`            Abandoned multipart session
                                      retention period.

  `OWNS3_MAX_METADATA_SIZE`           Maximum user metadata size.

  `OWNS3_REQUEST_TIMEOUT`             Request timeout.
  -----------------------------------------------------------------------

Names are proposed; align them with existing configuration conventions
before implementation. Validate invalid or missing settings at startup
and fail with an actionable message.

------------------------------------------------------------------------

## 8. Testing and compatibility strategy

V2 is complete only when behavior is covered by automated tests.

### 8.1 Unit tests

Cover isolated logic such as:

-   Continuation-token encoding and validation.
-   Prefix and delimiter grouping.
-   Range parsing and byte-bound calculations.
-   SigV4 canonical request construction and signature verification.
-   Checksum calculation and comparison.
-   Multipart manifest validation and part ordering.
-   Metadata normalization and validation.
-   S3 error-code/status mapping.
-   Storage path mapping and traversal prevention.

### 8.2 HTTP integration tests

Use Go's `net/http/httptest` and a temporary storage root/database test
setup to test complete request/response behavior.

Cover:

-   Multipart create/upload/complete/abort.
-   ListParts and ListMultipartUploads.
-   ListObjectsV2 filtering, delimiter, and pagination.
-   CopyObject and metadata directives.
-   DeleteObjects verbose and quiet modes.
-   Checksums and invalid checksums.
-   Range GET.
-   User metadata on PUT, HEAD, GET, and COPY.
-   Atomic overwrite under interrupted uploads.
-   Size-limit enforcement.
-   Consistent XML errors and HEAD behavior.

### 8.3 SDK and CLI compatibility tests

Maintain a documented manual or automated test matrix for:

  -----------------------------------------------------------------------
  Client                              Test
  ----------------------------------- -----------------------------------
  AWS SDK for Go v2                   PutObject, GetObject, HeadObject,
                                      ListObjectsV2, DeleteObjects,
                                      multipart upload, presigned
                                      GET/PUT.

  AWS CLI                             `cp` small file, `cp` large file,
                                      `ls` with prefix, `rm --recursive`,
                                      presigned URL workflows where
                                      supported.

  Second SDK/client                   Basic CRUD, listing, multipart
                                      upload, and presigned URL if
                                      practical.
  -----------------------------------------------------------------------

Record client versions, OwnS3 configuration, exact commands, and
outcomes. Do not declare compatibility based solely on one SDK version.

### 8.4 Regression requirements

-   Every bug fixed in V2 must gain a regression test.
-   Existing V1 tests must continue to pass unless behavior is
    intentionally corrected.
-   Tests must not depend on public AWS infrastructure.
-   Tests must clean up temporary files, buckets, objects, and multipart
    sessions.
-   CI should run unit and integration tests on every change.

------------------------------------------------------------------------

## 9. Priorities and implementation phases

The sequence below aims to deliver compatibility value early while
avoiding building advanced features on unreliable storage primitives.

### Phase 0 --- Foundations and contracts

**Deliverables**

-   Review current routes, schema, storage implementation, and error
    handling.
-   Define object-size and request-size configuration.
-   Introduce shared S3 error serialization.
-   Design safe temporary-file and atomic-publication flow.
-   Define multipart storage layout and recovery behavior.
-   Add baseline integration-test helpers.

**Exit criteria**

-   All implemented routes use shared S3 errors.
-   Size limits are configurable and enforced.
-   Object writes use safe publication or have a clearly isolated
    migration plan.

### Phase 1 --- Listing and everyday object operations

**Deliverables**

-   Complete ListObjectsV2: prefix, delimiter, max-keys,
    continuation-token, start-after.
-   Implement DeleteObjects.
-   Implement CopyObject, including metadata directive behavior.
-   Add user-defined object metadata.

**Exit criteria**

-   Prefix/delimiter/pagination tests pass.
-   Recursive AWS CLI deletion works.
-   Copy and metadata behavior passes integration tests.

### Phase 2 --- Multipart upload

**Deliverables**

-   CreateMultipartUpload.
-   UploadPart.
-   CompleteMultipartUpload.
-   AbortMultipartUpload.
-   ListParts.
-   ListMultipartUploads.
-   Abandoned-upload cleanup.

**Exit criteria**

-   Multipart lifecycle tests pass.
-   Large AWS SDK and CLI uploads pass.
-   Failed and abandoned uploads do not leave unbounded temporary data.

### Phase 3 --- SDK upload integrity and streaming compatibility

**Deliverables**

-   Content-MD5 validation.
-   Supported flexible checksum algorithms and headers.
-   BadDigest handling.
-   Compatibility work for aws-chunked where required by the selected
    SDK baseline.
-   Bounded streaming request processing.

**Exit criteria**

-   Stock SDK upload defaults pass.
-   Invalid checksums fail without publishing an object.
-   Memory remains bounded for large uploads.

### Phase 4 --- Authentication and delegated access

**Deliverables**

-   Explicit open/SigV4 configuration.
-   SigV4 header authentication.
-   Presigned GET and PUT.
-   Authentication error mapping and security tests.

**Exit criteria**

-   Valid signed requests pass.
-   Invalid, expired, and tampered requests fail.
-   Open mode is explicit and documented.

### Phase 5 --- Range retrieval and hardening

**Deliverables**

-   Single-range GET support.
-   Atomic copy and multipart publication review.
-   Crash recovery and orphan cleanup.
-   Operational logging and request IDs.
-   Full compatibility regression run.

**Exit criteria**

-   Range tests pass.
-   Failure-injection tests demonstrate safe object publication.
-   Compatibility matrix is green for the declared clients and
    operations.

------------------------------------------------------------------------

## 10. Definition of Done

V2 is done when all of the following are true:

-   [ ] ListObjectsV2 supports prefix, delimiter, max-keys,
    continuation-token, and start-after.
-   [ ] Multipart create, part upload, complete, abort, ListParts, and
    ListMultipartUploads work.
-   [ ] AWS SDK and CLI large-file uploads work with the declared
    default configuration.
-   [ ] CopyObject works for same-bucket and cross-bucket copies.
-   [ ] DeleteObjects supports up to 1,000 keys and Quiet mode.
-   [ ] Supported checksum headers are validated; mismatches return
    `BadDigest`.
-   [ ] Presigned GET and PUT URLs work and reject tampering/expiration.
-   [ ] SigV4 mode verifies signatures; open mode is explicit.
-   [ ] Range GET returns correct 206/416 behavior.
-   [ ] User metadata persists and is returned by GET/HEAD.
-   [ ] PutObject, CopyObject, and multipart completion publish objects
    atomically.
-   [ ] Request, object, part, metadata, and multipart limits are
    configurable and enforced.
-   [ ] Failed and abandoned uploads are cleaned up safely.
-   [ ] S3 routes return consistent XML errors, including correct HEAD
    behavior.
-   [ ] Unit, integration, failure-injection, and declared SDK/CLI
    compatibility tests pass.
-   [ ] Documentation lists implemented operations, unsupported
    behavior, configuration, and reproducible client examples.

------------------------------------------------------------------------

## 11. Explicitly deferred beyond V2

Unless a concrete compatibility test demonstrates an immediate need,
defer:

-   Object versioning and delete markers.
-   Lifecycle rules and automatic tiering.
-   Cross-region or cross-bucket replication.
-   ACLs and IAM-style bucket policies.
-   Virtual-hosted-style bucket addressing.
-   Multi-node clustering and distributed consistency.
-   Object Lock, legal hold, and retention policies.
-   Event notifications.
-   Website hosting.
-   Select and advanced S3 analytics APIs.
-   Full multi-range GET responses.
-   Full AWS account, billing, and control-plane behavior.

These features should not displace the V2 blockers above.

## 12. Risks and design decisions to resolve

Before or during implementation, make and document these decisions:

1.  **Multipart part storage:** whether each part is stored as a
    separate temporary file or appended to a multipart staging file.
    Separate files simplify replacement and retries; assembly requires a
    streaming concatenation step.
2.  **Multipart ETag semantics:** define the ETag calculation for
    completed multipart objects and ensure clients do not assume it is a
    plain MD5 of the final object.
3.  **Filesystem/database consistency:** define recovery for crashes
    between file rename and PostgreSQL commit.
4.  **Continuation-token stability:** choose an opaque token format and
    define behavior when objects change between pages.
5.  **Checksum baseline:** use actual supported SDK traces/tests to
    decide which checksum algorithms and streaming modes are mandatory.
6.  **SigV4 scope:** decide whether V2 supports only a single configured
    credential or a small credential store. Keep the first
    implementation intentionally narrow.
7.  **Cross-bucket copy:** decide whether it is included in the first
    CopyObject milestone or follows same-bucket copy.
8.  **CORS:** presigned browser uploads may also need configurable CORS
    responses. Treat CORS as a separate feature with its own tests
    rather than assuming presigned URLs alone solve browser access.
9.  **Object size defaults:** choose defaults based on expected local
    workloads and filesystem/database constraints; document how to raise
    them.
10. **Durability mode:** decide whether file and directory fsync are
    mandatory, configurable, or best-effort.

------------------------------------------------------------------------

## 13. V2 outcome

OwnS3 V2 should move the project from a working S3-shaped CRUD
implementation to a dependable local S3-compatible service for common
application development and integration testing.

The defining success test is practical: an application using a
mainstream AWS SDK should be able to configure OwnS3 as its endpoint and
perform ordinary small uploads, large multipart uploads, prefix
listings, copies, batch deletes, presigned transfers, and range
downloads without special workarounds---within the explicitly documented
compatibility boundary.
