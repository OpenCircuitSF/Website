package inbound

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeS3 is a minimal in-memory s3API double — no network, no AWS
// credentials, matching AGENTS.md §10's "develop against mocks" for
// anything gated on #0057's not-yet-created bucket.
type fakeS3 struct {
	objects map[string][]byte
	deleted []string
	buckets []string

	getErr    error
	deleteErr error
}

func (f *fakeS3) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.buckets = append(f.buckets, aws.ToString(params.Bucket))
	if f.getErr != nil {
		return nil, f.getErr
	}
	body, ok := f.objects[aws.ToString(params.Key)]
	if !ok {
		return nil, errors.New("NoSuchKey")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (f *fakeS3) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.buckets = append(f.buckets, aws.ToString(params.Bucket))
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, aws.ToString(params.Key))
	delete(f.objects, aws.ToString(params.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func TestS3Store_FetchAndDelete(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{"unsubscribe/msg-1": []byte("hello")}}
	store := NewS3StoreForTesting(fake, "test-bucket")

	body, err := store.Fetch(context.Background(), "unsubscribe/msg-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("Fetch body = %q, want %q", body, "hello")
	}

	if err := store.Delete(context.Background(), "unsubscribe/msg-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != "unsubscribe/msg-1" {
		t.Errorf("deleted = %v, want [unsubscribe/msg-1]", fake.deleted)
	}
	if _, ok := fake.objects["unsubscribe/msg-1"]; ok {
		t.Error("object still present after Delete")
	}
}

func TestS3Store_FetchMissingKey(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}}
	store := NewS3StoreForTesting(fake, "test-bucket")

	if _, err := store.Fetch(context.Background(), "unsubscribe/missing"); err == nil {
		t.Fatal("Fetch: want error for missing key, got nil")
	}
}

// TestS3Store_FetchNoSuchKey_MapsToSentinel pins #0500's mapping: GetObject's
// typed *types.NoSuchKey — as the SDK actually returns it, wrapped in an
// operation error — crosses Fetch as ErrNoSuchKey, with the bucket/key
// context kept in the message so the handler's log line stays useful. This
// is what lets internal/handlers route a missing object to its parked path
// through the narrow inboundObjectStore interface without naming any SDK
// type.
func TestS3Store_FetchNoSuchKey_MapsToSentinel(t *testing.T) {
	fake := &fakeS3{getErr: fmt.Errorf("operation error S3: GetObject, %w", &types.NoSuchKey{})}
	store := NewS3StoreForTesting(fake, "test-bucket")

	_, err := store.Fetch(context.Background(), "unsubscribe/missing")
	if !errors.Is(err, ErrNoSuchKey) {
		t.Fatalf("Fetch err = %v, want ErrNoSuchKey", err)
	}
	if !strings.Contains(err.Error(), "s3://test-bucket/unsubscribe/missing") {
		t.Errorf("Fetch err message = %q, want it to keep the bucket/key context", err)
	}

	// The mapping keys on the SDK's TYPED error, not on an error string: an
	// ordinary error that merely spells "NoSuchKey" must not match, or any
	// unrelated failure carrying that word in its text would silently take
	// the parked path instead of the retryable 500.
	fake2 := &fakeS3{getErr: errors.New("NoSuchKey")}
	store2 := NewS3StoreForTesting(fake2, "test-bucket")
	if _, err := store2.Fetch(context.Background(), "unsubscribe/other"); errors.Is(err, ErrNoSuchKey) {
		t.Error("a plain error spelling NoSuchKey must not be reported as ErrNoSuchKey — only the SDK's typed error maps")
	}
}

func TestS3Store_FetchTooLarge(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{"big": make([]byte, maxObjectBytes+1)}}
	store := NewS3StoreForTesting(fake, "test-bucket")

	_, err := store.Fetch(context.Background(), "big")
	if !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("Fetch err = %v, want ErrObjectTooLarge", err)
	}
}

func TestS3Store_DeleteError(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}, deleteErr: errors.New("boom")}
	store := NewS3StoreForTesting(fake, "test-bucket")

	if err := store.Delete(context.Background(), "unsubscribe/msg-1"); err == nil {
		t.Fatal("Delete: want error, got nil")
	}
}

// SetBucketSource makes Fetch and Delete follow a live bucket name.
func TestS3Store_BucketSourceIsLive(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{"k": []byte("x")}}
	store := NewS3StoreForTesting(fake, "static-bucket")
	name := "first-bucket"
	store.SetBucketSource(func() string { return name })
	if _, err := store.Fetch(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	name = "second-bucket"
	if err := store.Delete(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if len(fake.buckets) != 2 || fake.buckets[0] != "first-bucket" || fake.buckets[1] != "second-bucket" {
		t.Errorf("buckets = %v", fake.buckets)
	}
}
