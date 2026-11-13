// NoSuchBucket	404	Code, Message, BucketName, RequestId, HostId
// NoSuchKey	404	Code, Message, Key, RequestId, HostId
// BucketNotEmpty	409	Code, Message, BucketName, RequestId, HostId
// InvalidArgument	400	Code, Message, ArgumentName, ArgumentValue, RequestId, HostId
// EntityTooLarge	400	Code, Message, RequestId, HostId
// BadDigest	400	Code, Message, ExpectedDigest, CalculatedDigest, RequestId, HostId
// InvalidRange	416	Code, Message, RequestId, HostId
// AccessDenied	403	Code, Message, RequestId, HostId
// SignatureDoesNotMatch	403	Code, Message, AWSAccessKeyId, StringToSign, SignatureProvided, StringToSignBytes, RequestId, HostId
// InvalidAccessKeyId	403	Code, Message, AWSAccessKeyId, RequestId, HostId
// MethodNotAllowed	405	Code, Message, Method, Resource, RequestId, HostId
// NotImplemented	501	Code, Message, RequestId, HostId
// InternalError	500	Code, Message, RequestId, HostId

package main

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"main/internal/database"
	"main/internal/middleware"
	"main/internal/responses"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// declare queries
var queries *database.Queries

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

var buckets []string

const bucketsDir = "./internal/storage/buckets"

var database_url string = os.Getenv("DATABASE_URL")

// request id function

func requestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// write xml (for reponse body)
func writeXML(w http.ResponseWriter, status int, v any) {
	body, err := xml.Marshal(v)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	w.Write([]byte(xml.Header))
	w.Write(body)
}

// write error

func writeError(w http.ResponseWriter, status int, e responses.Error) {
	e.RequestID = w.Header().Get("x-amz-request-id")
	e.HostID = w.Header().Get("x-amz-id-2")
	writeXML(w, status, e)
}

// write common heards
func commonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("x-amz-request-id", requestID())
		h.Set("x-amz-id-2", requestID())
		h.Set("Server", "OwnS3")
		next.ServeHTTP(w, r)
	})
}

// internal server error fucntion
const (
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	reset  = "\033[0m"
)

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s ERROR INTERNAL === request_id=%s %s %s: %v %s", red,
		w.Header().Get("x-amz-request-id"), r.Method, r.URL.Path, err, reset)

	if r.Method == http.MethodHead {
		w.WriteHeader(500) // HEAD: no body
		return
	}
	writeError(w, 500, responses.Error{
		Code:    "InternalError",
		Message: "We encountered an internal error. Please try again.",
	})
}

// validate Bucket Name
func validateBucketName(name string, w http.ResponseWriter) bool {
	message := ""

	if len(name) < 3 || len(name) > 63 {
		message = "name must be 3-63 chars"
	}
	// only lowercase, digits, dots, hyphens
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*[a-z0-9]$`).MatchString(name) {
		message = "invalid characters or start/end"
	}
	if strings.Contains(name, "..") {
		message = ("no adjacent periods")
	}
	if strings.HasPrefix(name, "xn--") ||
		strings.HasPrefix(name, "sthree-") ||
		strings.HasPrefix(name, "amzn-s3-demo-") {
		message = ("reserved prefix")
	}
	if strings.HasSuffix(name, "-s3alias") ||
		strings.HasSuffix(name, "--ol-s3") ||
		strings.HasSuffix(name, "--x-s3") ||
		strings.HasSuffix(name, "--table-s3") {
		message = ("reserved suffix")
	}
	// check not IP-like (4 dot-separated numbers)
	if regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(name) {
		message = "cannot be IP format"
	}

	if message == "" {
		return true
	}

	e := responses.Error{
		Code:       responses.ErrInvalidBucketName,
		Message:    message,
		BucketName: name,
	}

	writeError(w, 400, e)

	return false

}

// get ping route
// mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
// 	fmt.Fprint(w, "pong")
// })

// handlers or routes

//object routes

// put object route requirements
// Functional requirements
// Your upload endpoint should handle these operations:
// #	Requirement	Description
// 1	Extract bucket	Get bucket name from URL
// 2	Extract key	Get complete object key, including nested paths
// 3	Validate bucket	Check whether bucket exists in PostgreSQL
// 4	Validate key	Ensure the object key is valid and safe
// 5	Receive file	Read binary data from r.Body
// 6	Create directories	Create nested directories if required
// 7	Store object	Write binary data to filesystem
// 8	Calculate size	Track the number of bytes uploaded
// 9	Calculate checksum	Calculate MD5 or another supported checksum
// 10	Save metadata	Insert/update object metadata in PostgreSQL
// 11	Handle overwrites	Replace an existing object with the same key
// 12	Return response	Return appropriate HTTP status and ETag

// put object route
func PutObjectHandler(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	// params object
	params := database.CreateObjectParams{}

	// 	Extract bucket name and key from URL
	bucketName := r.PathValue("bucket")
	key := r.PathValue("key")
	headerContentType := r.Header.Get("Content-Type")

	// validate bucket name
	valid := validateBucketName(bucketName, w)
	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}

	body := r.Body
	defer body.Close()

	if headerContentType == "" {
		headerContentType = "application/octet-stream"
	}
	contentType := pgtype.Text{
		String: headerContentType,
		Valid:  true,
	}

	// set params
	params.ObjectKey = key
	params.ContentType = contentType

	// 3	Validate bucket	check whether bucket exists in PostgreSQL
	bucket, err := queries.GetBucketByName(ctx, bucketName)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Printf("bucket does not exist %v", err)
			e := responses.Error{
				Code:       responses.ErrNoSuchBucket,
				Message:    "Bucket does not exist",
				BucketName: bucketName,
			}
			writeError(w, http.StatusNotFound, e)
			return
		}

		log.Printf("bucket validation failed %v", err)
		internalError(w, r, err)
		return
	}

	params.BucketID = bucket.ID

	// 4	Validate object key	Ensure the object key is valid and safe

	getObjectParams := database.GetObjectByBucketAndKeyParams{
		BucketID:  bucket.ID,
		ObjectKey: key,
	}

	objectExists := true
	object, err := queries.GetObjectByBucketAndKey(ctx, getObjectParams)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			objectExists = false
		} else {
			log.Printf("Internal server error , %v", err)
			internalError(w, r, err)
			return
		}
	}

	if objectExists {
		params.StorageKey = object.StorageKey
	} else {
		params.StorageKey = pgtype.UUID{
			Bytes: uuid.New(),
			Valid: true,
		}
	}

	// 5	parse object stream and receive file	Read binary data from r.Body
	// if err := r.ParseMultipartForm(50 << 20); err != nil {
	// 	http.Error(w, "object parsing from req failed", http.StatusBadRequest)
	// 	log.Fatal("object parsing from req failed  %v ", err)
	// 	return
	// }

	// file, _, err := r.FormFile("file")
	// defer file.Close()

	// 6	create directories	and create nested directories if required
	fileName := params.StorageKey.String()
	dirName := fileName[:2]

	dest := filepath.Join(bucketsDir, dirName, fileName)

	destDir := filepath.Dir(dest)

	if err := os.MkdirAll(destDir, 0775); err != nil {
		log.Printf("Failed to create dir: %v", err)
		internalError(w, r, err)
		return
	}

	// Create destination file
	destObj, err := os.Create(dest)

	if err != nil {
		log.Printf("Error while creating destination %v", err)
		internalError(w, r, err)
		return
	}
	defer destObj.Close()

	hash := md5.New()

	// save body data to dst
	bytes_size, err := io.Copy(io.MultiWriter(destObj, hash), body)
	if err != nil {
		log.Printf("Error while saving the object %v", err)
		internalError(w, r, err)
		return
	}

	params.SizeBytes = bytes_size

	etag := fmt.Sprintf("\"%x\"", hash.Sum(nil))

	params.Etag = pgtype.Text{
		String: etag,
		Valid:  true,
	}

	if objectExists {
		updateParams := database.UpdateObjectParams{
			ID:          object.ID,
			SizeBytes:   params.SizeBytes,
			ContentType: params.ContentType,
			Etag:        params.Etag,
		}
		_, err := queries.UpdateObject(ctx, updateParams)
		if err != nil {
			internalError(w, r, err)
			return
		}

	} else {

		_, err = queries.CreateObject(ctx, params)
		if err != nil {
			os.Remove(dest)
			log.Printf("Error while saving file to db %v", err)
			internalError(w, r, err)
		}

	}

	// return final put object
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)

}

// get object route i.e. download object
// mux.HandleFunc("GET /{bucket}/{key...}",
func GetObjectHandler(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	params := database.GetObjectByBucketAndKeyParams{}

	bucketName := r.PathValue("bucket")
	key := r.PathValue("key")
	valid := validateBucketName(bucketName, w)

	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}
	params.ObjectKey = key

	// 3	Validate bucket	check whether bucket exists in PostgreSQL
	bucket, err := queries.GetBucketByName(ctx, bucketName)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// missing bucket get object
		e := responses.Error{
			Code:       responses.ErrNoSuchBucket,
			Message:    "Bucket does not exist",
			BucketName: bucketName,
		}
		writeError(w, http.StatusNotFound, e)
		return
	case err != nil:
		internalError(w, r, err)
		return
	}

	params.BucketID = bucket.ID

	object, err := queries.GetObjectByBucketAndKey(ctx, params)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// missing object
		e := responses.Error{
			Code:    responses.ErrNoSuchKey,
			Message: "Key does not exist!",
			Key:     key,
		}
		writeError(w, http.StatusNotFound, e)
		return
	case err != nil:
		internalError(w, r, err)
		return
	}

	fileName := object.StorageKey.String()
	dirName := fileName[:2]

	dst := filepath.Join(bucketsDir, dirName, fileName)

	file, err := os.Open(dst)

	if err != nil {
		internalError(w, r, err)
		return
	}
	defer file.Close()

	// contentType := mime.TypeByExtension(filepath.Ext(key))

	// final response get object
	w.Header().Set("Content-Type", object.ContentType.String)
	w.Header().Set("ETag", object.Etag.String)
	w.Header().Set("Last-Modified", object.UpdatedAt.Time.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusOK)
	// Accept-Ranges: bytes, Content-Range (206 only), x-amz-meta-*

	_, err = io.Copy(w, file)

	if err != nil {
		log.Printf("failed to send object %v ", err)
		return
	}

}

// head object route i.e. get metadata of object
// mux.HandleFunc("HEAD /{bucket}/{key...}",
func HeadObjectHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params := database.GetObjectByBucketAndKeyParams{}

	bucketName := r.PathValue("bucket")
	key := r.PathValue("key")
	valid := validateBucketName(bucketName, w)

	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}
	params.ObjectKey = key
	// 3	Validate bucket	check whether bucket exists in PostgreSQL
	bucket, err := queries.GetBucketByName(ctx, bucketName)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		http.Error(w, "bucket does not exist", http.StatusNotFound)
		return
	case err != nil:
		internalError(w, r, err)
		return
	}

	params.BucketID = bucket.ID

	object, err := queries.GetObjectByBucketAndKey(ctx, params)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		http.Error(w, "object does not exist", http.StatusNotFound)
		return
	case err != nil:
		internalError(w, r, err)
		return
	}

	// final response head object
	w.Header().Set("Content-Type", object.ContentType.String)
	w.Header().Set("ETag", object.Etag.String)
	w.Header().Set("Last-Modified", object.UpdatedAt.Time.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusOK)

}

// delete object from a bucket route
// mux.HandleFunc("DELETE /{bucket}/{key}",
func DeleteObjectHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params := database.GetObjectByBucketAndKeyParams{}

	bucketName := r.PathValue("bucket")
	key := r.PathValue("key")
	valid := validateBucketName(bucketName, w)

	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}
	params.ObjectKey = key

	// 3	Validate bucket	check whether bucket exists in PostgreSQL
	bucket, err := queries.GetBucketByName(ctx, bucketName)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		e := responses.Error{
			Code:       responses.ErrNoSuchBucket,
			Message:    "Bucket does not exist",
			BucketName: bucketName,
		}

		writeError(w, 404, e)
		return
	case err != nil:
		internalError(w, r, err)
		return
	}

	params.BucketID = bucket.ID

	object, err := queries.GetObjectByBucketAndKey(ctx, params)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		w.WriteHeader(http.StatusNoContent)
		return
	case err != nil:
		internalError(w, r, err)
		return
	}

	var deleteParams database.DeleteObjectByBucketAndKeyParams

	deleteParams.BucketID = params.BucketID
	deleteParams.ObjectKey = params.ObjectKey

	if err := queries.DeleteObjectByBucketAndKey(ctx, deleteParams); err != nil {
		internalError(w, r, err)
		return
	}

	fileName := object.StorageKey.String()
	dirName := fileName[:2]

	dst := filepath.Join(bucketsDir, dirName, fileName)

	if err := os.Remove(dst); err != nil {
		internalError(w, r, err)
		return
	}

	// final response delete object
	w.WriteHeader(http.StatusNoContent)

	//end
}

// bucket routes

// head bucket route
// mux.HandleFunc("HEAD /{bucket}",
func HeadBucketHanlder(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	bucketName := r.PathValue("bucket")
	valid := validateBucketName(bucketName, w)

	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}
	// head bucket db query
	bucket, err := queries.GetBucketByName(ctx, bucketName)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Bucket Does Not Exist", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to fetch bucket from DB", http.StatusInternalServerError)
		return
	}

	log.Println("Bucket found successfully")
	log.Println(bucket)
	w.Header().Set("x-amz-bucket-region", "us-east-1")
	w.WriteHeader(http.StatusOK)
}

// get objects in bucket
// mux.HandleFunc("GET /{bucket}",
func GetObjectsHandler(w http.ResponseWriter, r *http.Request) {

	log.Println("List Objects in Bucket called")
	ctx := r.Context()
	bucketName := r.PathValue("bucket")

	valid := validateBucketName(bucketName, w)

	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}
	bucket, err := queries.GetBucketByName(ctx, bucketName)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			e := responses.Error{
				Code:       responses.ErrNoSuchBucket,
				Message:    "bucket does not exist",
				BucketName: bucketName,
			}

			writeError(w, http.StatusNotFound, e)
			return
		}
		internalError(w, r, err)
		return
	}

	objects, err := queries.GetObjectsByBucket(ctx, bucket.ID)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		internalError(w, r, err)
		return
	}

	// final response get objects
	res := responses.ListBucketResult{
		Name:        bucketName,
		MaxKeys:     1000,
		KeyCount:    min(len(objects), 1000),
		Prefix:      "",
		IsTruncated: len(objects) > 1000,
	}

	for i, object := range objects {
		if i >= 1000 {
			break
		}
		res.Contents = append(res.Contents,
			responses.Object{
				Key:          object.ObjectKey,
				LastModified: object.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05.000Z"),
				ETag:         object.Etag.String,
				Size:         object.SizeBytes,
				StorageClass: "STANDARD",
			},
		)
	}

	sort.Slice(res.Contents, func(i, j int) bool {
		return res.Contents[i].Key < res.Contents[j].Key
	})

	writeXML(w, http.StatusOK, res)
}

// get buckets list routes
// mux.HandleFunc("GET /",
func GetBucketsHanlder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// get buckets db query
	buckets, err := queries.GetBuckets(ctx)

	if err != nil {
		http.Error(w, "Fetching buckets from db failed", http.StatusInternalServerError)
		return
	}

	res := responses.ListAllMyBucketsResult{
		Owner: responses.Owner{
			ID:          "owner-id",
			DisplayName: "owner-display-name",
		},
	}

	for _, bucket := range buckets {
		res.Buckets.Bucket = append(res.Buckets.Bucket, responses.Bucket{
			Name:         bucket.Name,
			CreationDate: bucket.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	writeXML(w, http.StatusOK, res)

}

// put bucket route
// mux.HandleFunc("PUT /{bucket}",
func PutBucketHandler(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()
	bucketName := r.PathValue("bucket")

	valid := validateBucketName(bucketName, w)

	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}

	// Check whether bucket exists
	exists, err := queries.ExistsBucketByName(ctx, bucketName)

	if err != nil {
		internalError(w, r, err)
		return
	}

	// if bucket already exists
	// BucketAlreadyExists	409	Code, Message, BucketName, RequestId, HostId
	// BucketAlreadyOwnedByYou	409	Code, Message, BucketName, RequestId, HostId
	if exists {
		e := responses.Error{
			Code:       responses.ErrBucketAlreadyOwnedByYou,
			Message:    "Bucket already exsits owned by you.",
			BucketName: bucketName,
		}
		writeError(w, 409, e)
		return
	}

	// Create bucket
	_, err = queries.CreateBucket(ctx, bucketName)

	if err != nil {
		log.Printf("error creating bucket: %v", err)
		http.Error(w, "Failed to create bucket", http.StatusInternalServerError)
		return
	}

	// final reponse put bucket

	w.Header().Set("Location", "/"+bucketName)
	w.WriteHeader(http.StatusOK)
	return
}

// mux.HandleFunc("PUT /{bucket}", func(w http.ResponseWriter, r *http.Request) {
// 	// fetch bucketId from params
// 	bucketName := r.PathValue("bucket")

// 	//check bucket name in database
// 	if err != nil {
// 		log.Fatal("error while finding bucketName in db", err)
// 		fmt.Fprint(w, "error while find bucket in db")
// 		return
// 	}

// 	if exists {
// 		fmt.Fprint(w, "bucket exists")
// 		return
// 	}

// 	if err != nil {
// 		log.Fatal("error while creating bucket", err)
// 		fmt.Fprint(w, "error while creating bucket")
// 	}

// 	fmt.Fprint(w, "bucket created sucessfull.", "bucket name: ", createBucket.Name, "bucket id: ", createBucket.ID)
// })

// delete bucket route
// mux.HandleFunc("DELETE /{bucket}",
func DeleteBucketHandler(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()

	// request params
	bucketName := r.PathValue("bucket")

	valid := validateBucketName(bucketName, w)

	// InvalidBucketName	400	Code, Message, BucketName, RequestId, HostId
	if !valid {
		return
	}

	bucket, err := queries.GetBucketByName(ctx, bucketName)
	if err != nil {
		// missing bucket delete bucket
		if errors.Is(err, pgx.ErrNoRows) {
			e := responses.Error{
				Code:       responses.ErrNoSuchBucket,
				Message:    responses.ErrNoSuchBucket,
				BucketName: bucketName,
			}
			writeError(w, http.StatusNotFound, e)
			return
		} else {
			internalError(w, r, err)
			return
		}
	}

	isBucketEmtpy, err := queries.IsBucketEmpty(ctx, bucket.ID)

	if err != nil {
		internalError(w, r, err)
		return
	}

	if !isBucketEmtpy {
		e := responses.Error{
			Code:       responses.ErrBucketNotEmpty,
			Message:    "Can't delete a non empty bucket!",
			BucketName: bucketName,
		}

		writeError(w, http.StatusConflict, e)
		return
	}

	if err := queries.DeleteBucketByName(ctx, bucketName); err != nil {
		internalError(w, r, err)
		return
	}

	dest := filepath.Join(bucketsDir, bucketName)
	if err = os.RemoveAll(dest); err != nil {
		internalError(w, r, err)
		return
	}

	// final response delete bucket
	w.WriteHeader(204)

}

// main function
func main() {
	mux := http.NewServeMux()

	fmt.Println("DATABASE_URl", database_url)

	//pool creation
	pool, err := pgxpool.New(context.Background(), database_url)

	if err != nil {
		log.Fatal("error while creating pool", err)
	}

	queries = database.New(pool)

	// pool pinging
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		log.Fatal("Error while pinging pool", err)
	} else {
		fmt.Print("pool pinging was successfull")
	}

	// map routes or map handlers
	mux.HandleFunc("HEAD /{$}", func(w http.ResponseWriter, r *http.Request) {

		w.WriteHeader(http.StatusMethodNotAllowed)
		// e := responses.Error{
		// 	Code:    responses.ErrMethodNotAllowed,
		// 	Message: "This route is not implemented",
		// }
		// writeError(w, http.StatusMethodNotAllowed, e)
	})
	mux.HandleFunc("GET /{$}", GetBucketsHanlder)
	mux.HandleFunc("PUT /{bucket}", PutBucketHandler)
	mux.HandleFunc("GET /{bucket}", GetObjectsHandler)
	mux.HandleFunc("HEAD /{bucket}", HeadBucketHanlder)
	mux.HandleFunc("DELETE /{bucket}", DeleteBucketHandler)

	mux.HandleFunc("PUT /{bucket}/{key...}", PutObjectHandler)
	mux.HandleFunc("GET /{bucket}/{key...}", GetObjectHandler)
	mux.HandleFunc("HEAD /{bucket}/{key...}", HeadObjectHandler)
	mux.HandleFunc("DELETE /{bucket}/{key...}", DeleteObjectHandler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		e := responses.Error{
			Code:    responses.ErrMethodNotAllowed,
			Message: "This route is not implemented",
		}
		writeError(w, http.StatusMethodNotAllowed, e)

	})

	addr := ":9000"

	//start server
	log.Printf("Server listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, middleware.CORSMiddleware(loggingMiddleware(commonHeaders(mux)))); err != nil {
		log.Fatal(err)
	}
}
