// Package s3 writes billing records to an S3 bucket.
package s3

import (
	"bytes"
	"context"
	"fmt"
	"net/url"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Writer puts billing records into one bucket.
type Writer struct {
	client *minio.Client
	bucket string
}

// New connects to endpoint and checks that bucket exists.
func New(ctx context.Context, endpoint, bucket string) (*Writer, error) {
	if bucket == "" {
		return nil, fmt.Errorf("s3 bucket is not set")
	}

	host, secure, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parsing s3 endpoint: %w", err)
	}

	client, err := minio.New(host, &minio.Options{Secure: secure, Creds: credentials.NewEnvAWS()})
	if err != nil {
		return nil, fmt.Errorf("creating s3 client: %w", err)
	}

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("checking s3 bucket %s: %w", bucket, err)
	}

	if !exists {
		return nil, fmt.Errorf("s3 bucket %s does not exist", bucket)
	}

	return &Writer{
		client: client,
		bucket: bucket,
	}, nil
}

// Put stores body under key.
func (w *Writer) Put(ctx context.Context, key string, body []byte) error {
	_, err := w.client.PutObject(ctx, w.bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		return fmt.Errorf("putting %s: %w", key, err)
	}

	return nil
}

// parseEndpoint splits an endpoint URL into the host minio wants and
// whether to use TLS.
func parseEndpoint(endpoint string) (string, bool, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", false, fmt.Errorf("parsing endpoint url: %w", err)
	}

	var secure bool
	switch parsed.Scheme {
	case "https":
		secure = true
	case "http":
		secure = false
	default:
		return "", false, fmt.Errorf("endpoint %q must start with http:// or https://", endpoint)
	}

	if parsed.Host == "" {
		return "", false, fmt.Errorf("endpoint %q has no host", endpoint)
	}

	return parsed.Host, secure, nil
}
