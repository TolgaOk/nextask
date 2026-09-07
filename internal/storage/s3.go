package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/TolgaOk/nextask/internal/buildinfo"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var ErrNotFound = errors.New("object not found")

type Object struct {
	Size   int64
	SHA256 string
}

// Store is the transport boundary used by the upload policy.
type Store interface {
	Stat(context.Context, string) (Object, error)
	Put(context.Context, string, io.Reader, int64, string, string) error
}

// DownloadStore provides streaming reads and paginated object discovery.
type DownloadStore interface {
	List(context.Context, string, func(string) error) error
	Get(context.Context, string) (io.ReadCloser, Object, error)
}

// Client supports both upload and download policies.
type Client interface {
	Store
	DownloadStore
}

type s3Store struct {
	client *minio.Client
	bucket string
}

// NewS3 constructs a transport from an endpoint already resolved by the integration.
func NewS3(c Config, resolvedEndpoint string) (Client, error) {
	connection, err := url.Parse(resolvedEndpoint)
	if err != nil || connection.User == nil {
		return nil, fmt.Errorf("invalid storage endpoint")
	}
	access := connection.User.Username()
	secret, _ := connection.User.Password()
	client, err := minio.New(connection.Host, &minio.Options{
		Secure: connection.Scheme == "https", Region: c.Region,
		Creds: credentials.NewStaticV4(access, secret, ""), MaxRetries: c.Retries + 1,
	})
	if err != nil {
		return nil, err
	}
	client.SetAppInfo("nextask", buildinfo.Version)
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

func (s *s3Store) List(ctx context.Context, prefix string, visit func(string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for info := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if info.Err != nil {
			return info.Err
		}
		if err := visit(info.Key); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, Object{}, err
	}
	info, err := object.Stat()
	if err != nil {
		object.Close()
		return nil, Object{}, err
	}
	return object, Object{Size: info.Size, SHA256: info.Metadata.Get("X-Amz-Meta-Nextask-Sha256")}, nil
}
