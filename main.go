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

	addr := ":9000"
	log.Printf("Server listening on http://localhost%s", addr)

	if err := http.ListenAndServe(addr, loggingMiddleware(mux)); err != nil {
		log.Fatal(err)
	}
}
