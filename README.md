# OwnS3

**Self-hosted, S3-compatible object storage you can run in production.**

OwnS3 gives your applications a familiar Amazon S3 API while keeping every byte and every metadata row on infrastructure you control. Point any AWS SDK or S3 client at your OwnS3 endpoint — no vendor lock-in, no surprise cloud bills, no opaque storage layer.

Built for teams that need durable object storage for backups, media, artifacts, and application data — with the operational simplicity of a single Go binary, PostgreSQL, and a disk.

```bash
docker compose up
# API ready at http://localhost:9000
```

---

## Why production teams choose OwnS3

| Concern | How OwnS3 handles it |
|---|---|
| **Data durability** | Object bytes live on your filesystem; metadata lives in PostgreSQL. Both survive process restarts. |
| **Integrity** | Every upload computes an MD5 ETag. Clients can verify what they wrote. |
| **Safe concurrency** | Go’s `net/http` serves concurrent requests; PostgreSQL connection pooling keeps metadata access stable under load. |
| **Memory safety** | Uploads and downloads stream — large objects never require loading the full body into RAM. |
| **Path safety** | Logical object keys are never used as filesystem paths. Objects are stored under UUID-based storage keys with sharded directories. |
| **API correctness** | Responses follow the S3 REST contract (status codes, headers, XML errors). Unsupported features fail explicitly — never silently. |
| **Deployability** | One `docker compose up` brings up the API, PostgreSQL, and Swagger UI. |
| **Client compatibility** | Verified against the AWS SDK for Go v2 through an integration test suite. |

OwnS3 is designed as a **focused, single-node S3 subset** — the operations most applications actually use — implemented carefully enough to trust in production.

---

## Features

### Bucket operations
- Create, list, head, and delete buckets
- S3-compatible bucket name validation
- Refuse deletion of non-empty buckets

### Object operations
- Put, get, head, and delete objects
- List objects in a bucket
- Content-Type preservation
- ETag on every successful put / get / head
- Keys with nested prefixes (`photos/2024/avatar.png`)

### Platform
- Path-style addressing (`http://host:9000/{bucket}/{key}`)
- Streaming I/O for large objects
- Structured request logging and `x-amz-request-id` on responses
- CORS middleware for browser clients
- OpenAPI 3.1 contract + Swagger UI
- Goose migrations + sqlc-typed queries

---

## Architecture

```text
        AWS SDK / AWS CLI / your app
                    │
                    ▼
           ┌─────────────────┐
           │  OwnS3 (Go)     │
           │  S3 REST API    │
           └────────┬────────┘
                    │
          ┌─────────┴─────────┐
          ▼                   ▼
   PostgreSQL            Filesystem
   (metadata)            (object bytes)
```

| Layer | Responsibility |
|---|---|
| **HTTP / S3 API** | Path-style routing, XML responses, S3 error shapes |
| **PostgreSQL** | Buckets, object keys, sizes, content types, ETags, timestamps |
| **Filesystem** | Durable object payloads under `internal/storage/buckets/` |

Logical keys and physical paths are deliberately decoupled. A key like `../../etc/passwd` cannot escape the storage root.

---

## Quick start

### Prerequisites
- Docker & Docker Compose
- (Optional) Go 1.26+ for local development outside containers

### 1. Start the stack

```bash
git clone https://github.com/Raman-Khiva/ownS3.git
cd ownS3
docker compose up --build
```

| Service | URL |
|---|---|
| OwnS3 API | http://localhost:9000 |
| Swagger UI | http://localhost:8080 |
| PostgreSQL | `localhost:5432` |

### 2. Apply migrations

```bash
make migrate-up
```

### 3. Verify with the AWS CLI

```bash
aws --endpoint-url http://localhost:9000 s3 mb s3://demo
echo "hello production" > hello.txt
aws --endpoint-url http://localhost:9000 s3 cp hello.txt s3://demo/hello.txt
aws --endpoint-url http://localhost:9000 s3 cp s3://demo/hello.txt -
```

### 4. Or use any AWS SDK

```go
cfg, _ := config.LoadDefaultConfig(ctx,
    config.WithRegion("us-east-1"),
    config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
        "your-access-key", "your-secret-key", "",
    )),
)

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String("http://localhost:9000")
    o.UsePathStyle = true
})

_, err := client.PutObject(ctx, &s3.PutObjectInput{
    Bucket: aws.String("demo"),
    Key:    aws.String("reports/q1.pdf"),
    Body:   bytes.NewReader(pdfBytes),
})
```

Path-style addressing is required (`forcePathStyle: true` / `UsePathStyle = true`).

---

## Supported S3 API

| Operation | Method | Path |
|---|---|---|
| `ListBuckets` | `GET` | `/` |
| `CreateBucket` | `PUT` | `/{bucket}` |
| `HeadBucket` | `HEAD` | `/{bucket}` |
| `DeleteBucket` | `DELETE` | `/{bucket}` |
| `ListObjects` | `GET` | `/{bucket}` |
| `PutObject` | `PUT` | `/{bucket}/{key}` |
| `GetObject` | `GET` | `/{bucket}/{key}` |
| `HeadObject` | `HEAD` | `/{bucket}/{key}` |
| `DeleteObject` | `DELETE` | `/{bucket}/{key}` |

Full request/response shapes are documented in [`openapi.yaml`](./openapi.yaml) and [`AWS-COMPATIBLE-API.md`](./AWS-COMPATIBLE-API.md).

Operations outside this surface return clear S3-style errors rather than pretending to succeed.

---

## Configuration

| Variable | Purpose | Default / notes |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection string | Set in `docker-compose.yaml` |
| Listen address | API bind address | `:9000` |

Compose defaults for local/dev:

```text
DATABASE_URL=postgres://ownS3:password@postgres:5432/ownS3?sslmode=disable
```

For production, override credentials, enable TLS to PostgreSQL (`sslmode=require`), and mount a dedicated volume for object data.

---

## Production deployment

OwnS3 is production-ready for **single-node** workloads when you follow these practices:

1. **Persistent volumes**  
   Mount durable storage for both PostgreSQL (`postgres_data`) and the object directory (`internal/storage/buckets`). Losing either volume means losing data.

2. **Backups**  
   - Database: standard PostgreSQL backups (`pg_dump`, continuous archiving).  
   - Objects: filesystem snapshots or rsync of the storage directory.  
   Treat metadata and object bytes as a pair — restore them together.

3. **Network**  
   Terminate TLS at a reverse proxy (nginx, Caddy, Traefik) in front of `:9000`. Keep PostgreSQL off the public internet.

4. **Resources**  
   Size disk for expected object growth. Memory stays modest because transfers stream; CPU scales with concurrent connections.

5. **Observability**  
   Request logs include method, path, and latency. Correlate client issues with `x-amz-request-id`.

6. **Credentials**  
   Rotate access keys, restrict who can reach the API, and never commit secrets to git.

```bash
# Useful ops commands
make compose-up          # start stack
make compose-logs s=api  # follow API logs
make migrate-up          # apply DB migrations
make clean-storage       # wipe local object files (dev only)
```

---

## Testing & confidence

OwnS3 ships with AWS SDK integration tests that exercise real S3 client behavior against a live server — not mocks.

```bash
# With OwnS3 running on :9000
cd tests/sdk-integration
S3_ENDPOINT=http://localhost:9000 go test -v -count=1 ./v1/
```

The suite covers the core bucket/object lifecycle that production applications depend on: create → put → get → head → list → delete, plus validation and error-contract checks.

Design docs and contracts:

- [`StacknRequirements.md`](./StacknRequirements.md) — architecture & requirements
- [`AWS-COMPATIBLE-API.md`](./AWS-COMPATIBLE-API.md) — S3 API compatibility target
- [`owns3-s3-response-contract.md`](./owns3-s3-response-contract.md) — HTTP/XML response contract
- [`openapi.yaml`](./openapi.yaml) — machine-readable API spec

---

## Project layout

```text
ownS3/
├── main.go                 # S3 HTTP handlers & server
├── internal/
│   ├── database/           # sqlc-generated PostgreSQL access
│   ├── middleware/         # CORS and shared HTTP concerns
│   ├── responses/          # S3 XML success & error models
│   └── storage/buckets/    # On-disk object payloads
├── migrations/             # Goose SQL migrations
├── queries/                # sqlc query definitions
├── tests/sdk-integration/  # AWS SDK compatibility tests
├── openapi.yaml            # OpenAPI 3.1 contract
├── Dockerfile
└── docker-compose.yaml
```

---

## Compatibility scope

OwnS3 implements the S3 operations most applications need for day-to-day object storage. It is **not** a drop-in clone of every Amazon S3 feature.

Intentionally out of scope today (and must fail clearly if requested):

- Object versioning, ACLs, and bucket policies  
- Lifecycle rules, replication, and notifications  
- Virtual-hosted–style addressing  
- Multi-node clustering / erasure coding  

If your workload is “store and retrieve objects through the AWS SDK on infrastructure I own,” OwnS3 is built for that — and built to do it reliably.

---

## License

See the repository for license details.

---

**Your infrastructure. Your objects. Your control.**
