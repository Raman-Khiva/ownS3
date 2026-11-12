package responses

import (
	"encoding/xml"
)

const NS = "http://s3.amazonaws.com/doc/2006-03-01/"

// Errors (no namespace)
type Error struct {
	XMLName          xml.Name `xml:"Error"`
	Code             string   `xml:"Code"`
	Message          string   `xml:"Message"`
	BucketName       string   `xml:"BucketName,omitempty"`
	Key              string   `xml:"Key,omitempty"`
	ArgumentName     string   `xml:"ArgumentName,omitempty"`
	ArgumentValue    string   `xml:"ArgumentValue,omitempty"`
	RangeRequested   string   `xml:"RangeRequested,omitempty"`
	ActualObjectSize int64    `xml:"ActualObjectSize,omitempty"`
	RequestID        string   `xml:"RequestId"`
	HostID           string   `xml:"HostId,omitempty"`
}

// ListBuckets
type ListAllMyBucketsResult struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListAllMyBucketsResult"`
	Owner   Owner    `xml:"Owner"`
	Buckets Buckets  `xml:"Buckets"`
}
type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}
type Buckets struct {
	Bucket []Bucket `xml:"Bucket"`
}
type Bucket struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

// ListObjectsV2
type ListBucketResult struct {
	XMLName               xml.Name       `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
	Name                  string         `xml:"Name"`
	Prefix                string         `xml:"Prefix"`
	Delimiter             string         `xml:"Delimiter,omitempty"`
	MaxKeys               int            `xml:"MaxKeys"`
	EncodingType          string         `xml:"EncodingType,omitempty"`
	KeyCount              int            `xml:"KeyCount"`
	IsTruncated           bool           `xml:"IsTruncated"`
	ContinuationToken     string         `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
	StartAfter            string         `xml:"StartAfter,omitempty"`
	Contents              []Object       `xml:"Contents"`
	CommonPrefixes        []CommonPrefix `xml:"CommonPrefixes"`
}
type Object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}
type CommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

// -----------------------------------------------------------------------------
// GET /{bucket}?list-type=2 — ListObjectsV2
// -----------------------------------------------------------------------------

// type ListObjectsV2Result struct {
// 	XMLName               xml.Name       `xml:"ListBucketResult"`
// 	XMLNS                 string         `xml:"xmlns,attr,omitempty"`
// 	Name                  string         `xml:"Name"`
// 	Prefix                string         `xml:"Prefix"`
// 	Delimiter             string         `xml:"Delimiter,omitempty"`
// 	StartAfter            string         `xml:"StartAfter,omitempty"`
// 	KeyCount              int            `xml:"KeyCount"`
// 	MaxKeys               int            `xml:"MaxKeys"`
// 	IsTruncated           bool           `xml:"IsTruncated"`
// 	ContinuationToken     string         `xml:"ContinuationToken,omitempty"`
// 	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
// 	Contents              []Object       `xml:"Contents,omitempty"`
// 	CommonPrefixes        []CommonPrefix `xml:"CommonPrefixes,omitempty"`
// 	EncodingType          string         `xml:"EncodingType,omitempty"`
// 	FetchOwner            bool           `xml:"FetchOwner,omitempty"`
// 	RequestCharged        string         `xml:"RequestCharged,omitempty"`
// }

// type Object struct {
// 	Key               string    `xml:"Key"`
// 	LastModified      time.Time `xml:"LastModified"`
// 	ETag              string    `xml:"ETag"`
// 	ChecksumAlgorithm []string  `xml:"ChecksumAlgorithm,omitempty"`
// 	ChecksumType      string    `xml:"ChecksumType,omitempty"`
// 	Size              int64     `xml:"Size"`
// 	StorageClass      string    `xml:"StorageClass"`
// 	Owner             *Owner    `xml:"Owner,omitempty"`
// }

// type CommonPrefix struct {
// 	Prefix string `xml:"Prefix"`
// }

// type Owner struct {
// 	ID          string `xml:"ID"`
// 	DisplayName string `xml:"DisplayName,omitempty"`
// }

// -----------------------------------------------------------------------------
// S3 XML errors — use for error responses with a body.
// For HEAD requests, send the status and headers without an XML response body.
// -----------------------------------------------------------------------------

// type Error struct {
// 	XMLName   xml.Name `xml:"Error"`
// 	Code      string   `xml:"Code"`
// 	Message   string   `xml:"Message"`
// 	Resource  string   `xml:"Resource,omitempty"`
// 	RequestID string   `xml:"RequestId,omitempty"`
// 	HostID    string   `xml:"HostId,omitempty"`
// }

// Common S3 error codes. The Error.Code field intentionally remains a string
// so OwnS3 can support additional S3-compatible codes without changing this API.
const (
	ErrAccessDenied            = "AccessDenied"
	ErrBucketAlreadyExists     = "BucketAlreadyExists"
	ErrBucketAlreadyOwnedByYou = "BucketAlreadyOwnedByYou"
	ErrBucketNotEmpty          = "BucketNotEmpty"
	ErrEntityTooLarge          = "EntityTooLarge"
	ErrIncompleteBody          = "IncompleteBody"
	ErrInternalError           = "InternalError"
	ErrInvalidArgument         = "InvalidArgument"
	ErrInvalidBucketName       = "InvalidBucketName"
	ErrInvalidDigest           = "InvalidDigest"
	ErrInvalidRange            = "InvalidRange"
	ErrInvalidRequest          = "InvalidRequest"
	ErrMalformedXML            = "MalformedXML"
	ErrMethodNotAllowed        = "MethodNotAllowed"
	ErrNoSuchBucket            = "NoSuchBucket"
	ErrNoSuchKey               = "NoSuchKey"
	ErrNotImplemented          = "NotImplemented"
	ErrPreconditionFailed      = "PreconditionFailed"
	ErrSignatureDoesNotMatch   = "SignatureDoesNotMatch"
	ErrSlowDown                = "SlowDown"
	ErrTooManyBuckets          = "TooManyBuckets"
)

// -----------------------------------------------------------------------------
// Success-body markers for the remaining operations.
// These operations do not marshal XML success bodies.
// -----------------------------------------------------------------------------

// EmptyBody is a marker for operations whose successful response body is empty.
// Examples: CreateBucket, DeleteBucket, HeadBucket, PutObject, HeadObject,
// and DeleteObject. Return the appropriate HTTP status and headers separately.
type EmptyBody struct{}

// GetObject writes the stored object bytes directly to the HTTP response.
// It is deliberately not represented as an XML model.
