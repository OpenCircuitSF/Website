package inbound

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
)

// s3API is the subset of *s3.Client this package calls, mirroring
// internal/mailing's sesAPI seam: defining it locally, rather than
// depending on the concrete *s3.Client everywhere, is what lets
// internal/handlers depend on a narrow interface for S3 access
// (CLAUDE.md §1) instead of the SDK client reaching a handler signature —
// see this issue's design notes. Tests inject a fake that never dials AWS.
type s3API interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// maxObjectBytes bounds how much of a fetched object Fetch will read.
// SES's own receiving limit is 40 MB (30 MB before its 2023 increase); an
// unsubscribe reply is plain text of a few KB, so 5 MiB is a generous
// ceiling that still refuses a pathological or maliciously large object
// rather than reading it all into memory.
const maxObjectBytes = 5 << 20

// S3Store fetches and deletes inbound-mail objects in one S3 bucket
// (#0058, PRD §6.5 path 3: s3://opencircuitsf-inbound/unsubscribe/*).
// Credentials come from the AWS SDK's default credential chain — the EC2
// instance role in production, falling back to ~/.aws/credentials locally —
// the same as internal/mailing.SESMailer; there is no static-credential
// configuration here either.
type S3Store struct {
	client s3API
	bucket string
}

// NewS3Store constructs an S3Store backed by a real S3 client, using
// cfg.AWSRegion (required config, so always set) and cfg.SESInboundBucket
// (#0057's bucket name — optional in internal/config, since the bucket
// does not exist yet: CLAUDE.md §10). An empty bucket is not itself a
// construction error, matching sesnotify.NewVerifier's empty-topic-ARN
// convention: the endpoint should still start, and simply have nothing to
// fetch successfully until the bucket is configured — Fetch/Delete calls
// against an empty bucket name fail at the AWS SDK, logged by the caller,
// not at boot.
func NewS3Store(ctx context.Context, cfg *config.Config) (*S3Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("inbound: loading AWS config: %w", err)
	}
	return &S3Store{
		client: s3.NewFromConfig(awsCfg),
		bucket: cfg.SESInboundBucket,
	}, nil
}

// NewS3StoreForTesting builds an S3Store around a fake s3API, for tests
// that need to exercise Fetch/Delete without a network dependency (mirrors
// internal/mailing's NewSESMailerForTesting-shaped seams for the same
// reason: CLAUDE.md §10's "develop against mocks", since the bucket #0057
// creates does not exist yet).
func NewS3StoreForTesting(client s3API, bucket string) *S3Store {
	return &S3Store{client: client, bucket: bucket}
}

// ErrObjectTooLarge is returned by Fetch when the object exceeds
// maxObjectBytes.
var ErrObjectTooLarge = errors.New("inbound: object exceeds the size limit")

// Fetch retrieves the object at key and returns its full body. Bounded by
// maxObjectBytes: an oversized read returns ErrObjectTooLarge rather than
// consuming unbounded memory on a webhook endpoint that carries no
// authentication of its own beyond #0037's SNS signature check.
func (s *S3Store) Fetch(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("inbound: fetching s3://%s/%s: %w", s.bucket, key, err)
	}
	defer out.Body.Close()

	limited := io.LimitReader(out.Body, maxObjectBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("inbound: reading s3://%s/%s: %w", s.bucket, key, err)
	}
	if len(body) > maxObjectBytes {
		return nil, fmt.Errorf("inbound: s3://%s/%s: %w", s.bucket, key, ErrObjectTooLarge)
	}
	return body, nil
}

// Delete removes the object at key — called only after it has been fully
// processed (matched and unsubscribed, or correctly identified as an
// auto-reply/no-match and left for the OTHER path, never this one; see
// SESInboundHandler's doc comment for exactly which outcomes delete).
func (s *S3Store) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("inbound: deleting s3://%s/%s: %w", s.bucket, key, err)
	}
	return nil
}
