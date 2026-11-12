package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				"test-access-key",
				"test-secret-key",
				"",
			),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://localhost:9000")
		o.UsePathStyle = true
	})

	bucket := "sdk-test-bucket-6"
	key := "hello.txt"
	content := "Hello from AWS SDK to ownS3!"

	// 1. CreateBucket
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	check("CreateBucket", err)

	// 2. PutObject
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(content),
	})
	check("PutObject", err)

	// 3. GetObject
	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	check("GetObject", err)

	body, err := io.ReadAll(result.Body)
	result.Body.Close()
	check("ReadObjectBody", err)

	fmt.Println("Downloaded:", string(body))

	// 4. HeadObject
	head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	check("HeadObject", err)

	fmt.Println("ContentLength:", *head.ContentLength)

	// 5. ListObjectsV2
	listing, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	check("ListObjectsV2", err)

	for _, object := range listing.Contents {
		fmt.Println("Object:", *object.Key)
	}

	// 6. DeleteObject
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	check("DeleteObject", err)

	// 7. DeleteBucket
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	check("DeleteBucket", err)

	fmt.Println("All SDK operations completed!")
}

func check(operation string, err error) {
	if err != nil {
		log.Fatalf("%s failed: %v", operation, err)
	}

	fmt.Println(operation, "SUCCESS")
}
