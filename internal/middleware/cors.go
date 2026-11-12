package middleware

import (
	"net/http"

	"github.com/rs/cors"
)

func CORSMiddleware(next http.Handler) http.Handler {
	c := cors.New(cors.Options{
		AllowedOrigins: []string{
			"http://localhost:8080",
		},
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodPut,
			http.MethodPost,
			http.MethodDelete,
			http.MethodHead,
			http.MethodOptions,
		},
		AllowedHeaders: []string{
			"Content-Type",
			"Authorization",
			"Content-MD5",
			"X-Amz-Date",
			"X-Amz-Content-Sha256",
			"X-Amz-Security-Token",
			"X-Amz-Meta-*",
		},
		ExposedHeaders: []string{
			"ETag",
			"Last-Modified",
			"Content-Length",
			"x-amz-request-id",
		},
		AllowCredentials: false,
		MaxAge:           3600,
	})

	return c.Handler(next)
}
