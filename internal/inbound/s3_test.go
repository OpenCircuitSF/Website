package inbound

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeS3 is a minimal in-memory s3API double — no network, no AWS
// credentials, matching CLAUDE.md §10's "develop against mocks" for
// anything gated on #0057's not-yet-created bucket.
type fakeS3 struct {
	objects map[string][]byte
	deleted []string

	getErr    error
	deleteErr error
}

func (f *fakeS3) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
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
