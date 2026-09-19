package media

import (
	"context"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Object is an opened stored file. *minio.Object satisfies io.ReadSeeker, which
// is what lets http.ServeContent honour Range requests (video seeking).
type Object struct {
	io.ReadSeekCloser
	Size    int64
	ModTime time.Time
}

// Store is object storage. The implementation talks S3, so the same code
// targets MinIO locally and S3/R2 in production.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Open(ctx context.Context, key string) (*Object, error)
	Delete(ctx context.Context, keys ...string) error
}

type s3Store struct {
	c      *minio.Client
	bucket string
}

// NewS3Store connects and makes sure the bucket exists.
func NewS3Store(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) (Store, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: useSSL})
	if err != nil {
		return nil, err
	}
	ok, err := c.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
	}
	return &s3Store{c: c, bucket: bucket}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.c.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *s3Store) Open(ctx context.Context, key string) (*Object, error) {
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	st, err := o.Stat() // fails here (not later mid-response) if the key is missing
	if err != nil {
		o.Close()
		return nil, err
	}
	return &Object{ReadSeekCloser: o, Size: st.Size, ModTime: st.LastModified}, nil
}

func (s *s3Store) Delete(ctx context.Context, keys ...string) error {
	var first error
	for _, k := range keys {
		if k == "" {
			continue
		}
		if err := s.c.RemoveObject(ctx, s.bucket, k, minio.RemoveObjectOptions{}); err != nil && first == nil {
			first = err
		}
	}
	return first
}
