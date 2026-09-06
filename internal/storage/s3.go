package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var ErrNotFound = errors.New("object not found")

type Object struct {
	Size   int64
	SHA256 string
}

// Store is the small transport boundary used by the upload policy.
type Store interface {
	Stat(context.Context, string) (Object, error)
	Put(context.Context, string, io.Reader, int64, string, string) error
}

type s3Store struct {
	client *minio.Client
	bucket string
}

func NewS3(c Config) (Store, error) {
	access, secret := os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY")
	if access == "" || secret == "" {
		return nil, fmt.Errorf("s3 requires S3_ACCESS_KEY and S3_SECRET_KEY in the worker environment")
	}
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid storage endpoint")
	}
	client, err := minio.New(endpoint.Host, &minio.Options{
		Secure: endpoint.Scheme == "https", Region: c.Region,
		Creds: credentials.NewStaticV4(access, secret, ""), MaxRetries: c.Retries + 1,
	})
	if err != nil {
		return nil, err
	}
	client.SetAppInfo("nextask", "0.2.0")
	return &s3Store{client: client, bucket: c.Bucket}, nil
}
func (s *s3Store) Stat(ctx context.Context, key string) (Object, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		switch minio.ToErrorResponse(err).Code {
		case "NoSuchKey", "NoSuchObject", "NotFound":
			return Object{}, ErrNotFound
		}
		return Object{}, err
	}
	return Object{Size: info.Size, SHA256: info.Metadata.Get("X-Amz-Meta-Nextask-Sha256")}, nil
}
func (s *s3Store) Put(ctx context.Context, key string, body io.Reader, size int64, digest, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, body, size, minio.PutObjectOptions{
		ContentType: contentType, UserMetadata: map[string]string{"nextask-sha256": digest},
		SendContentMd5: true, NumThreads: 1,
	})
	return err
}
