package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"main/internal/database"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "pong")
	})

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

		// 	Extract bucket name and key from URL
		bucketName := r.PathValue("bucket")
		key := r.PathValue("key")


		// 3	Validate bucket	check whether bucket exists in PostgreSQL
		bucket, err := queries.GetBucketByName(ctx,bucketName)

		switch{
			case errors.Is(err, sql.ErrNoRows):
				http.Error(w,"bucket does not exist", http.StatusNotFound)
				return
			case err != nil{
				http.Error(w, "bucket name validation failed", http.StatusBadRequest)
				return
			}
		}


		// 4	Validate object key	Ensure the object key is valid and safe
		objectExists, err := queries.ObjectExsits(ctx, key,bucket.ID)
		if err:=nil{
			http.Error(w, "object key validation faild", http.StatusBadRequest)
			return
		}
		if(objectExists){
			http.Error(w, "object already exists in the bucket", http.StatusBadRequest)
			return
		}

		// 5	parse object stream and receive file	Read binary data from r.Body
		if err := r.ParseMultipartForm(50<<20); err!=nil{
			http.Error(w, "object parsing from req failed", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err!=nil{
			log.Fatal("Error while geting file from req :", err)
			http.Error(w, "errro while geting file from req", http.StatusBadRequest)
			return
		}
		defer file.Close()


		// 6	create directories	and create nested directories if required
		dst := filepath.Join(bucketsDir,bucketName, key)

		dstDir:= filepath.Dir(dst)

		if err := os.MkdirAll(dir,0775); err!=nil{
			http.Error(w, "failed to create dir", http.StatusInternalServerError)
			return
		}


		// Create destination file
		dstFile, err := os.Create(dst)
		if err != nil {
			http.Error(w, "error while creating destination", http.StatusInternalServerError)
			return
		}
		defer dstFile.Close()

		// save file data to dst
		if _, err := io.Copy(dstFile, file); err != nil {
			http.Error(w, "error while saving file to the dst", http.StatusInternalServerError)
			return
		}

		// return final
		fmt.Fprint(w, "Saved %s (%d bytes)", header.Filename, header.Size)

	})

	// get object route i.e. download object
	mux.HandleFunc("GET /{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Print("get object route called")

	})

	// head object route i.e. get metadata of object
	mux.HandleFunc("HEAD /{bucket}/{key...}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Print("head objects route called")
	})

	// delete object from a bucket route
	mux.HandleFunc("DELETE /{bucket}/{key}", func(w http.ResponseWriter, r *http.Request) {

	})

	// bucket routes

	// head bucket route
	// mux.HandleFunc("HEAD /{bucket}", func(w http.ResponseWriter, r *http.Request) {

	// 	fmt.Print("head objects route called")
	// })
	// get objects in bucket

	mux.HandleFunc("GET /{bucket}", func(w http.ResponseWriter, r *http.Request) {

	})

	//get buckets list routes
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {

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
		createdBucket, err := queries.CreateBucket(ctx, bucketName)

		if err != nil {
			log.Printf("error creating bucket: %v", err)
			http.Error(w, "Failed to create bucket", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w,
			"Bucket created successfully. Name: %s, ID: %v",
			createdBucket.Name,
			createdBucket.ID,
		)
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

	})

	addr := ":9000"
	log.Printf("Server listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, loggingMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}
