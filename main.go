package main

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"database/sql"
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
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	w.Header().Set("x-amz-request-id", requestID())
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
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("request_id=%s %s %s: %v",
		w.Header().Get("x-amz-request-id"), r.Method, r.URL.Path, err)

	if r.Method == http.MethodHead {
		w.WriteHeader(500) // HEAD: no body
		return
	}
	writeError(w, 500, responses.Error{
		Code:    "InternalError",
		Message: "We encountered an internal error. Please try again.",
	})
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

	queries := database.New(pool)

	// pool pinging
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		log.Fatal("Error while pinging pool", err)
	} else {
		fmt.Print("pool pinging was successfull")
	}

	// get ping route
	// mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
	// 	fmt.Fprint(w, "pong")
	// })

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
	mux.HandleFunc("PUT /{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {

		ctx := r.Context()

		// params object

		params := database.CreateObjectParams{}

		// 	Extract bucket name and key from URL
		bucketName := r.PathValue("bucket")
		key := r.PathValue("key")
		contentType := r.Header.Get("Content-Type")

		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// set params
		params.ObjectKey = key
		params.StorageKey = bucketName + "/" + key
		params.ContentType = pgtype.Text{
			String: contentType,
			Valid:  true,
		}

		body := r.Body
		defer body.Close()

		// 3	Validate bucket	check whether bucket exists in PostgreSQL
		bucket, err := queries.GetBucketByName(ctx, bucketName)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			log.Printf("bucket does not exist %v", err)
			http.Error(w, "bucket does not exist", http.StatusNotFound)
			return
		case err != nil:
			log.Printf("bucket validation failed %v", err)
			http.Error(w, "bucket name validation failed", http.StatusBadRequest)
			return
		}
		params.BucketID = bucket.ID

		// 4	Validate object key	Ensure the object key is valid and safe
		objectExistsParams := database.ObjectExsitsParams{}
		objectExistsParams.ObjectKey = key
		objectExistsParams.BucketID = bucket.ID
		objectExists, err := queries.ObjectExsits(ctx, objectExistsParams)
		if err != nil {
			log.Printf("object key validation  failed %v", err)
			http.Error(w, "object key validation faild", http.StatusBadRequest)
			return
		}
		if objectExists {
			log.Printf("object key already exists  %v ", err)
			http.Error(w, "object already exists in the bucket", http.StatusBadRequest)
			return
		}

		// 5	parse object stream and receive file	Read binary data from r.Body
		// if err := r.ParseMultipartForm(50 << 20); err != nil {
		// 	http.Error(w, "object parsing from req failed", http.StatusBadRequest)
		// 	log.Fatal("object parsing from req failed  %v ", err)
		// 	return
		// }

		// file, _, err := r.FormFile("file")

		if err != nil {
			log.Printf("Error while geting file from req %v", err)
			http.Error(w, "errro while geting file from req", http.StatusBadRequest)
			return
		}
		// defer file.Close()

		// 6	create directories	and create nested directories if required
		dst := filepath.Join(bucketsDir, bucketName, key)

		dstDir := filepath.Dir(dst)

		if err := os.MkdirAll(dstDir, 0775); err != nil {
			log.Printf("Failed to create dir: %v", err)
			http.Error(w, "failed to create dir", http.StatusInternalServerError)
			return
		}

		// Create destination file
		dstObj, err := os.Create(dst)
		if err != nil {
			log.Printf("Error while creating destination %v", err)
			http.Error(w, "error while creating destination", http.StatusInternalServerError)
			return
		}

		defer dstObj.Close()

		// save body data to dst
		bytes_size, err := io.Copy(dstObj, body)
		if err != nil {
			log.Printf("Error while saving the object %v", err)
			http.Error(w, "error while saving file to the dst", http.StatusInternalServerError)
			return
		}

		params.SizeBytes = bytes_size

		data, _ := io.ReadAll(r.Body)
		etag := fmt.Sprintf("\"%x\"", md5.Sum(data))
		params.Etag = pgtype.Text{
			String: etag,
			Valid:  true,
		}

		_, err = queries.CreateObject(ctx, params)

		if err != nil {
			os.Remove(dst)
			log.Printf("Error while saving file to db %v", err)
			http.Error(w, "failed to save file to db", http.StatusInternalServerError)
		}

		// return final put object
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)

	})

	// get object route i.e. download object
	mux.HandleFunc("GET /{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {

		ctx := r.Context()

		params := database.GetObjectByBucketAndKeyParams{}

		bucketName := r.PathValue("bucket")
		key := r.PathValue("key")

		params.ObjectKey = key

		// 3	Validate bucket	check whether bucket exists in PostgreSQL
		bucket, err := queries.GetBucketByName(ctx, bucketName)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "bucket does not exist", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "bucket name validation failed", http.StatusBadRequest)
			return
		}

		params.BucketID = bucket.ID

		object, err := queries.GetObjectByBucketAndKey(ctx, params)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "object does not exist", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "Failed while geting object from db", http.StatusBadRequest)
			return
		}

		dst := filepath.Join(bucketsDir, bucketName, key)

		file, err := os.Open(dst)

		if err != nil {
			http.Error(w, "Failed to get object from storage", http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// contentType := mime.TypeByExtension(filepath.Ext(key))

		// final reponse get object
		w.Header().Set("Content-Type", object.ContentType.String)
		w.Header().Set("ETag", object.Etag.String)
		w.Header().Set("Last-Modified", object.UpdatedAt.Time.UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
		w.Header().Set("Accpet-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		// Accept-Ranges: bytes, Content-Range (206 only), x-amz-meta-*

		_, err = io.Copy(w, file)

		if err != nil {
			log.Printf("failed to send object %v ", err)
			return
		}

	})

	// head object route i.e. get metadata of object
	mux.HandleFunc("HEAD /{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		params := database.GetObjectByBucketAndKeyParams{}

		bucketName := r.PathValue("bucket")
		key := r.PathValue("key")

		params.ObjectKey = key
		// 3	Validate bucket	check whether bucket exists in PostgreSQL
		bucket, err := queries.GetBucketByName(ctx, bucketName)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "bucket does not exist", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "bucket name validation failed", http.StatusBadRequest)
			return
		}

		params.BucketID = bucket.ID

		object, err := queries.GetObjectByBucketAndKey(ctx, params)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "object does not exist", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "Failed while geting object from db", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", object.ContentType.String)
		w.Header().Set("ETag", object.Etag.String)
		w.Header().Set("Last-Modified", object.UpdatedAt.Time.UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
		w.Header().Set("Accpet-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)

		w.WriteHeader(http.StatusOK)

	})

	// delete object from a bucket route
	mux.HandleFunc("DELETE /{bucket}/{key}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		params := database.GetObjectByBucketAndKeyParams{}

		bucketName := r.PathValue("bucket")
		key := r.PathValue("key")

		params.ObjectKey = key

		// 3	Validate bucket	check whether bucket exists in PostgreSQL
		bucket, err := queries.GetBucketByName(ctx, bucketName)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "bucket does not exist", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "bucket name validation failed", http.StatusBadRequest)
			return
		}

		params.BucketID = bucket.ID

		_, err = queries.GetObjectByBucketAndKey(ctx, params)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "object does not exist", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, "Failed while geting object from db", http.StatusBadRequest)
			return
		}

		var deleteParams database.DeleteObjectByBucketAndKeyParams

		deleteParams.BucketID = params.BucketID
		deleteParams.ObjectKey = params.ObjectKey

		if err := queries.DeleteObjectByBucketAndKey(ctx, deleteParams); err != nil {
			http.Error(w, "Failed to delete object", http.StatusInternalServerError)
			return
		}

		dst := filepath.Join(bucketsDir, bucketName, key)

		if err := os.Remove(dst); err != nil {
			http.Error(w, "Failed to delete object from storage", http.StatusInternalServerError)
			return
		}

		// final response delete object
		w.WriteHeader(http.StatusNoContent)

		//end
	})

	// bucket routes

	//head bucket route
	mux.HandleFunc("HEAD /{bucket}", func(w http.ResponseWriter, r *http.Request) {

		ctx := r.Context()

		bucketName := r.PathValue("bucket")

		// head bucket db query
		bucket, err := queries.GetBucketByName(ctx, bucketName)

		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Bucket Does Not Exist", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to fetch bucket from DB", http.StatusInternalServerError)
			return
		}

		log.Println("Bucket found successfully")
		log.Println(bucket)

		w.WriteHeader(http.StatusOK)
	})

	//get objects in bucket
	mux.HandleFunc("GET /{bucket}", func(w http.ResponseWriter, r *http.Request) {

		log.Println("List Objects in Bucket called")
		ctx := r.Context()
		bucketName := r.PathValue("bucket")

		bucket, err := queries.GetBucketByName(ctx, bucketName)

		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Bucket does not exist", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed while finding bucket from db", http.StatusInternalServerError)
			return
		}

		objects, err := queries.GetObjectsByBucket(ctx, bucket.ID)

		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Failed while fetching objects from db", http.StatusInternalServerError)
			return
		}

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

		writeXML(w, http.StatusOK, res)
	})

	//get buckets list routes
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
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

	})

	// put bucket route
	mux.HandleFunc("PUT /{bucket}", func(w http.ResponseWriter, r *http.Request) {

		bucketName := r.PathValue("bucket")
		ctx := r.Context()

		// Check whether bucket exists
		exists, err := queries.ExistsBucketByName(ctx, bucketName)

		if err != nil {
			log.Printf("error checking bucket: %v", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		if exists {
			http.Error(w, "Bucket already exists", http.StatusConflict)
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
	})
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
	mux.HandleFunc("DELETE /{bucket}", func(w http.ResponseWriter, r *http.Request) {

		ctx := r.Context()

		// request params
		bucketName := r.PathValue("bucket")

		bucket, err := queries.GetBucketByName(ctx, bucketName)
		if err != nil {
			if errors.As(err, sql.ErrNoRows) {
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

	})

	addr := ":9000"
	log.Printf("Server listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, middleware.CORSMiddleware(loggingMiddleware(commonHeaders(mux)))); err != nil {
		log.Fatal(err)
	}
}
