# oStore

**Self-hosted, S3-compatible object storage built with Go.**

oStore is a lightweight, single-node object storage server designed to provide an S3-compatible API while keeping object data on infrastructure you control.

It uses PostgreSQL for metadata management and the local filesystem for persistent object storage.

## Technology Stack

| Component | Technology | Purpose |
|---|---|---|
| Language | Go | Core storage server |
| HTTP Server | `net/http` | HTTP server and request handling |
| API | S3-compatible REST API | Object storage operations |
| Database | PostgreSQL | Bucket and object metadata |
| Database Driver | pgx | PostgreSQL connectivity |
| Object Storage | Local Filesystem | Persistent object bytes |
| File Operations | `os`, `io`, `path/filepath` | Filesystem management and streaming |
| Serialization | `encoding/xml`, `encoding/json` | S3 responses and configuration |
| Integrity | `crypto/sha256` | Object checksum calculation |
| Logging | `log/slog` | Structured logging |
| Configuration | Environment Variables | Runtime configuration |
| Containerization | Docker | Application containerization |
| Deployment | Docker Compose | Application and database orchestration |
| Version Control | Git & GitHub | Source control |

### Client & Testing Tools

| Technology | Purpose |
|---|---|
| AWS SDK for JavaScript v3 | S3 API compatibility testing |
| AWS CLI | Manual S3 operations |
| Go Testing | Unit and integration testing |
| `go test -race` | Race condition detection |
| Apache Bench / hey | Basic HTTP load testing |

---

## Architecture

oStore follows a modular monolithic architecture with separate metadata and object-data persistence.

```text
                    S3 Clients
             (AWS SDK / AWS CLI / Apps)
                       |
                       v
              +-------------------+
              |   HTTP Layer      |
              |     net/http      |
              +-------------------+
                       |
                       v
              +-------------------+
              |   S3 API Layer    |
              | Handlers / XML    |
              +-------------------+
                       |
                       v
              +-------------------+
              |   Core Services   |
              | Bucket / Object   |
              +-------------------+
                       |
                       v
              +-------------------+
              |  Storage Engine   |
              | Filesystem / Hash |
              +-------------------+
                    /       \
                   /         \
                  v           v
          +------------+  +------------+
          | PostgreSQL |  | Filesystem |
          |  Metadata  |  | Object Data|
          +------------+  +------------+
```

### Storage Responsibilities

**PostgreSQL**
- Bucket information and identifiers.
- Object keys and bucket associations.
- Physical storage locations.
- Object sizes and content types.
- ETags, checksums, and timestamps.

**Local Filesystem**
- Actual object bytes.
- Temporary upload files.
- Finalized object files.
- Persistent storage across server restarts.

Logical object keys are mapped to generated physical filenames. User-provided object keys are never used directly as filesystem paths.

---

## Functional Requirements

### 1. Bucket Management
- Create buckets.
- Delete empty buckets.
- Check bucket existence.
- List all buckets.
- Prevent duplicate bucket names.
- Validate bucket naming conventions.
- Prevent deletion of non-empty buckets.

### 2. Object Management
- Upload objects.
- Download objects.
- Delete objects.
- Replace existing objects.
- Retrieve object metadata.
- Check object existence.
- List objects within a bucket.
- Support prefix-based filtering.
- Support basic pagination.

### 3. Storage Engine
- Store object bytes on the local filesystem.
- Separate logical object keys from physical filenames.
- Stream uploads and downloads.
- Support large files without loading entire objects into memory.
- Handle concurrent reads and writes.
- Use temporary files for incomplete uploads.
- Finalize uploads through atomic file renaming.
- Clean up failed and incomplete uploads.

### 4. S3 API Compatibility

The initial implementation will support:

| S3 Operation | HTTP Method | Purpose |
|---|---|---|
| CreateBucket | PUT | Create a bucket |
| DeleteBucket | DELETE | Delete an empty bucket |
| ListBuckets | GET | List buckets |
| HeadBucket | HEAD | Check bucket existence |
| PutObject | PUT | Upload an object |
| GetObject | GET | Download an object |
| HeadObject | HEAD | Retrieve object metadata |
| DeleteObject | DELETE | Delete an object |
| ListObjectsV2 | GET | List objects with filtering and pagination |

Additional compatibility requirements:
- S3-compatible request routing.
- Path-style bucket addressing.
- Correct object-key URL decoding.
- S3-compatible XML responses.
- Appropriate HTTP status codes and headers.
- Standardized S3 error responses.
- Compatibility testing with AWS SDK for JavaScript v3.

**Authentication limitation:** The MVP will use configurable local access and secret credentials with basic validation. Full AWS Signature Version 4 authentication is outside the initial scope.

### 5. Security & Access
- Configurable access key and secret key.
- Basic request authentication.
- Request validation.
- Filesystem path traversal prevention.
- Configurable maximum request body size.
- Configurable listening address and port.
- Restrict unauthenticated access.

### 6. Performance
- Streaming I/O with bounded memory usage.
- Concurrent HTTP request handling.
- PostgreSQL connection pooling.
- Efficient metadata queries.
- Paginated object listing.
- HTTP read and write timeouts.

### 7. Reliability
- Temporary upload handling.
- Atomic file finalization.
- SHA-256 checksum calculation.
- Interrupted upload cleanup.
- Filesystem and database error handling.
- Metadata consistency checks.
- Graceful server shutdown.
- Recovery and cleanup of incomplete temporary uploads.

### 8. Observability
- Structured application logs.
- HTTP request logging.
- Request latency measurement.
- Error logging.
- Upload and download throughput measurement.
- Basic disk utilization reporting.
- Total object count and storage consumption.
- Health and readiness endpoints.

---

## Non-Functional Requirements

| Requirement | Expected Behavior |
|---|---|
| Memory efficiency | Object transfers must use streaming |
| Persistence | Objects and metadata survive server restarts |
| Concurrency | Multiple requests can be processed concurrently |
| Integrity | Object checksums are calculated and verified |
| Isolation | Bucket and object operations remain logically isolated |
| Maintainability | API, service, metadata, and storage layers remain separate |
| Portability | Deployment should work on Linux with Docker |
| Extensibility | Storage engine should be abstracted behind a Go interface |

---

## Deployment Requirements

### Runtime
- Linux server.
- Docker Engine.
- Docker Compose.
- Persistent disk volume for object data.
- Persistent PostgreSQL volume.

### Configuration

Runtime configuration will be provided through environment variables.

Initial configuration requirements:

| Variable | Purpose |
|---|---|
| `OSTORE_ADDR` | Server listening address |
| `OSTORE_PORT` | Server listening port |
| `OSTORE_DATA_DIR` | Root object storage directory |
| `DATABASE_URL` | PostgreSQL connection string |
| `OSTORE_ACCESS_KEY` | Local access key |
| `OSTORE_SECRET_KEY` | Local secret key |
| `OSTORE_MAX_OBJECT_SIZE` | Maximum permitted object size |

Exact defaults and configuration names will be finalized during implementation.

---

## Out of Scope — MVP

To keep the initial implementation focused, the following capabilities are deferred:

- Full AWS Signature Version 4 authentication.
- Multipart uploads.
- Presigned URLs.
- Object versioning.
- Lifecycle policies.
- Bucket replication.
- Distributed storage.
- Multi-node clustering.
- Erasure coding.
- Redis caching.
- Distributed rate limiting.
- Kubernetes orchestration.
- Advanced monitoring dashboards and distributed tracing.

---

## MVP Definition of Done

The initial version will be considered complete when:

1. The server starts and connects to PostgreSQL.
2. Buckets can be created, listed, and deleted.
3. Objects can be uploaded, downloaded, replaced, listed, and deleted.
4. Object bytes persist on the local filesystem.
5. Metadata persists in PostgreSQL.
6. Large uploads and downloads are streamed.
7. Supported S3 operations work through AWS SDK for JavaScript v3.
8. Concurrent operations do not introduce data races or corrupt object data.
9. Failed uploads do not leave visible incomplete objects.
10. Docker Compose can deploy the complete application.
11. Health checks and structured logs are available.
12. The project includes setup instructions and a working demonstration.

---

## Project Constraints

- **Architecture:** Single-node modular monolith.
- **Language:** Go.
- **Metadata:** PostgreSQL.
- **Object bytes:** Local filesystem.
- **Deployment:** Docker Compose.
- **Target:** Functional S3-compatible MVP.
- **Development budget:** Approximately 40 hours.

The MVP is intended to demonstrate the core engineering of an object storage system. It is not yet a production replacement for AWS S3 or a distributed storage platform.

## Project Philosophy

**Your infrastructure. Your control.**

oStore aims to make self-hosted object storage accessible through a familiar S3-compatible interface, with transparent filesystem persistence and minimal infrastructure dependencies.
