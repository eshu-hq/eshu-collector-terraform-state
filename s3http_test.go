// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestS3RequestURLEncodesRFC3986 proves keys and prefixes with spaces or
// plus signs encode as %20/%2B on the wire (not "+"), matching the SigV4
// canonical query exactly.
func TestS3RequestURLEncodesRFC3986(t *testing.T) {
	t.Parallel()

	reader := &HTTPS3Reader{endpoint: func(_, _ string) string { return "https://s3.example" }}
	target := reader.s3RequestURL("b", "a b+c", "r", url.Values{"prefix": {"x y"}, "list-type": {"2"}})
	want := "https://s3.example/a%20b%2Bc?list-type=2&prefix=x%20y"
	if target != want {
		t.Fatalf("s3RequestURL = %q, want %q", target, want)
	}
}

const s3ObjectFixture = `{"resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`

// s3TestServer serves one object GET and one prefix listing, asserting
// every request carries a SigV4 Authorization line and no credential.
func s3TestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential=AKID/") {
			t.Errorf("Authorization = %q, want the AKID SigV4 scope", authorization)
		}
		if request.Header.Get("X-Amz-Date") == "" {
			t.Errorf("X-Amz-Date missing on %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get("list-type") == "2" {
			writer.Header().Set("Content-Type", "application/xml")
			_, _ = writer.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Contents><Key>env/terraform.tfstate</Key></Contents><Contents><Key>prefix/</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`))
			return
		}
		if request.URL.Path != "/env/terraform.tfstate" {
			t.Errorf("path = %q, want /env/terraform.tfstate", request.URL.Path)
		}
		writer.Header().Set("ETag", `"abc123"`)
		_, _ = writer.Write([]byte(s3ObjectFixture))
	}))
}

func s3TestReader(server *httptest.Server) *HTTPS3Reader {
	return &HTTPS3Reader{
		Getenv: func(key string) string {
			if key == "AWS_ACCESS_KEY_ID" {
				return "AKID"
			}
			if key == "AWS_SECRET_ACCESS_KEY" {
				return "SECRET"
			}
			return ""
		},
		endpoint: func(_, _ string) string { return server.URL },
	}
}

// TestHTTPS3ReaderRoundTrip proves signed GET fetches the object with its
// ETag and listing returns exact keys while skipping prefix markers.
func TestHTTPS3ReaderRoundTrip(t *testing.T) {
	t.Parallel()

	server := s3TestServer(t)
	defer server.Close()
	reader := s3TestReader(server)

	object, err := reader.GetObject(context.Background(), "bg-ops-qa-terraform-state", "env/terraform.tfstate", "us-east-1")
	if err != nil {
		t.Fatalf("GetObject error = %v", err)
	}
	if string(object.Body) != s3ObjectFixture {
		t.Fatalf("Body = %q, want the fixture", object.Body)
	}
	if object.ETag != "abc123" {
		t.Fatalf("ETag = %q, want abc123", object.ETag)
	}
	keys, err := reader.ListKeys(context.Background(), "bg-ops-qa-terraform-state", "us-east-1", "")
	if err != nil {
		t.Fatalf("ListKeys error = %v", err)
	}
	if len(keys) != 1 || keys[0] != "env/terraform.tfstate" {
		t.Fatalf("keys = %q, want the one exact key", keys)
	}
}

// TestHTTPS3ReaderClassifiesFailures proves 404 maps to ErrStateMissing,
// throttles to ErrStateTransient, auth failures stay hard errors, and no
// error text carries the bucket or key.
func TestHTTPS3ReaderClassifiesFailures(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/xml")
		switch path := strings.TrimPrefix(request.URL.Path, "/"); {
		case strings.HasPrefix(path, "missing/"):
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`<Error><Code>NoSuchKey</Code></Error>`))
		case strings.HasPrefix(path, "bucket/"):
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`<Error><Code>NoSuchBucket</Code></Error>`))
		case strings.HasPrefix(path, "throttle/"):
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(`<Error><Code>SlowDown</Code></Error>`))
		default:
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
		}
	}))
	defer server.Close()
	reader := s3TestReader(server)
	missing, err := reader.GetObject(context.Background(), "secret-bucket", "missing/secret/key", "us-east-1")
	_ = missing
	if !errors.Is(err, ErrStateMissing) {
		t.Fatalf("GetObject(missing) error = %v, want ErrStateMissing", err)
	}
	if strings.Contains(err.Error(), "secret-bucket") || strings.Contains(err.Error(), "secret/key") {
		t.Fatalf("missing error leaks locator: %v", err)
	}
	_, bucketErr := reader.GetObject(context.Background(), "b", "bucket/k", "us-east-1")
	if bucketErr == nil || errors.Is(bucketErr, ErrStateMissing) {
		t.Fatalf("GetObject(NoSuchBucket) error = %v, want a hard config failure (not missing)", bucketErr)
	}
	if !strings.Contains(bucketErr.Error(), "does not exist") {
		t.Fatalf("GetObject(NoSuchBucket) error = %v, want the bucket-misconfiguration failure", bucketErr)
	}
	if _, err := reader.GetObject(context.Background(), "b", "throttle/k", "us-east-1"); !errors.Is(err, ErrStateTransient) {
		t.Fatalf("GetObject(throttle) error = %v, want ErrStateTransient", err)
	}
	denied, err := reader.GetObject(context.Background(), "b", "denied/k", "us-east-1")
	_ = denied
	if err == nil || errors.Is(err, ErrStateMissing) || errors.Is(err, ErrStateTransient) {
		t.Fatalf("GetObject(denied) error = %v, want a hard auth failure", err)
	}
}
