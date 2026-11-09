package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

var buckets []string

const bucketsDir = "./buckets"

func main() {
	mux := http.NewServeMux()
	// get ping route
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "pong")
	})



	//object routes

	// put object in bucket route
	mux.HandleFunc("PUT /{bucket}/{key}", func(w http.ResponseWriter, r *http.Request){

	})

	// get object route i.e. download object
	mux.HandleFunc("GET /{bucket}/{key}", func(w http.ResponseWriter, r *http.Request){

	})


	// head object route i.e. get metadata of object
	mux.HandleFunc("HEAD /{bucket}/{key}", func(w http.ResposneWriter, r * http.Request){

	})


	// delete object from a bucket route
	mux.HandleFunc("DELETE /{bucket}/{key}", func(w http.ResponseWriter, r* http.Request){

	})


	// bucket routes

	// head bucket route
		mux.HnadleFunc("HEAD /{bucket}", func(w http.ResponseWriter, r *http.Request)){

		}

	// get objects in bucket

		mux.HandleFunc("GET /{bucket}", func(w http.ResponseWriter, r *http.Request){

	})

	//get buckets list routes
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {

	})

	// put bucket route
	mux.HandleFunc("PUT /buckets/{bucketId}", func(w http.ResponseWriter, r *http.Request) {
		bucketId := r.PathValue("bucketId")

		dir := filepath.Join(bucketsDir, bucketId)

		var found bool = false

		for _, bucket := range buckets {
			if bucket == bucketId {
				found = true
				break
			}
		}

		if !found {
			buckets = append(buckets, bucketId)
			os.MkdirAll(dir, 0755)
		}

		var message string
		if found {
			message = "bucket already exists"
		} else {
			message = "bucket created successfully"
		}
		fmt.Fprint(w, message)
	})

	// delete bucket route
    mux.HandleFunc("DELETE /{bucket}",func(w http.ResponseWriter, r* http.Request){

    })


	addr := ":9000"
	log.Printf("Server listening on http://localhost%s", addr)

	if err := http.ListenAndServe(addr, loggingMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}
