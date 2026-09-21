package blobstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Store is byte storage for attachment originals. The database holds
// references (local paths or r2: keys); this moves the bytes. Two
// implementations: R2 in production, local disk wherever R2 is not
// configured (dev, tests) — the handlers branch on the reference prefix,
// never on environment, so both paths stay exercised.
type Store interface {
	Put(ctx context.Context, key string, body []byte, mime string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// R2Prefix marks references living in Cloudflare R2: "r2:<key>".
const R2Prefix = "r2:"

// R2Store speaks the S3 API against a Cloudflare R2 bucket.
type R2Store struct {
	client *s3.Client
	bucket string
	prefix string
}

// NewR2StoreFromEnv builds the R2 store from environment, reporting whether
// it is usable. All four of R2_ACCOUNT_ID, R2_ACCESS_KEY_ID,
// R2_SECRET_ACCESS_KEY, R2_BUCKET must be set; R2_PREFIX is optional and
// namespaces keys inside the bucket. Anything missing means R2 is dark and
// the caller falls back to local disk — never a half-configured client.
func NewR2StoreFromEnv() (*R2Store, bool) {
	account := strings.TrimSpace(os.Getenv("R2_ACCOUNT_ID"))
	keyID := strings.TrimSpace(os.Getenv("R2_ACCESS_KEY_ID"))
	secret := strings.TrimSpace(os.Getenv("R2_SECRET_ACCESS_KEY"))
	bucket := strings.TrimSpace(os.Getenv("R2_BUCKET"))
	if account == "" || keyID == "" || secret == "" || bucket == "" {
		return nil, false
	}
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("R2_PREFIX")), "/")
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider(keyID, secret, ""),
		BaseEndpoint: aws.String(fmt.Sprintf("https://%s.r2.cloudflarestorage.com", account)),
	})
	return &R2Store{client: client, bucket: bucket, prefix: prefix}, true
}

func (s *R2Store) key(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

func (s *R2Store) Put(ctx context.Context, key string, body []byte, mime string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(s.key(key)),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(mime),
	})
	if err != nil {
		return fmt.Errorf("r2 put %s: %w", key, err)
	}
	return nil
}

func (s *R2Store) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return nil, fmt.Errorf("r2 get %s: %w", key, err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(io.LimitReader(out.Body, 8<<20+1))
	if err != nil {
		return nil, fmt.Errorf("r2 read %s: %w", key, err)
	}
	return data, nil
}

func (s *R2Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return fmt.Errorf("r2 delete %s: %w", key, err)
	}
	return nil
}

// LocalStore keeps bytes under dir, keyed by relative path. The dev and test
// path — and the production fallback when R2 env is absent.
type LocalStore struct {
	Dir string
}

func (s LocalStore) Put(_ context.Context, key string, body []byte, _ string) error {
	path := filepath.Join(s.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

func (s LocalStore) Get(_ context.Context, key string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.Dir, filepath.FromSlash(key)))
}

func (s LocalStore) Delete(_ context.Context, key string) error {
	err := os.Remove(filepath.Join(s.Dir, filepath.FromSlash(key)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
