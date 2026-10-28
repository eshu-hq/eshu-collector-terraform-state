// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestResolveS3EndpointBlankSelectsDefault proves an unset or blank
// AWS_S3_ENDPOINT selects the regional AWS endpoints (empty base, no
// error) instead of failing or pointing at a zero endpoint.
func TestResolveS3EndpointBlankSelectsDefault(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "   "} {
		getenv := func(string) string { return value }
		base, err := ResolveS3Endpoint(getenv)
		if err != nil {
			t.Fatalf("ResolveS3Endpoint(%q) error = %v, want the AWS default", value, err)
		}
		if base != "" {
			t.Fatalf("ResolveS3Endpoint(%q) = %q, want empty for the AWS default", value, base)
		}
	}
}

// TestResolveS3EndpointTrimsTrailingSlash proves one cosmetic trailing
// slash (or several) never produces a double-slash request URL.
func TestResolveS3EndpointTrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	getenv := func(string) string { return "http://localhost:9000///" }
	base, err := ResolveS3Endpoint(getenv)
	if err != nil {
		t.Fatalf("ResolveS3Endpoint error = %v", err)
	}
	if base != "http://localhost:9000" {
		t.Fatalf("ResolveS3Endpoint = %q, want the trimmed base", base)
	}
}

// TestResolveS3EndpointRejectsUntrusted is the SSRF trust gate: only
// http(s) URLs without credentials, without query/fragment, away from
// instance metadata, and with http confined to loopback are accepted.
func TestResolveS3EndpointRejectsUntrusted(t *testing.T) {
	t.Parallel()

	rejected := map[string]string{
		"other scheme":          "ftp://localhost:9000/state",
		"missing scheme":        "localhost:9000/state",
		"userinfo":              "https://user:secret@localhost:9000/state",
		"bare userinfo":         "https://user@localhost:9000/",
		"metadata IPv4":         "http://169.254.169.254/latest/",
		"metadata HTTPS":        "https://169.254.169.254/latest/",
		"link-local sibling":    "http://169.254.169.253/",
		"link-local general":    "http://169.254.10.20/",
		"gcp metadata":          "http://metadata.google.internal/",
		"query smuggling":       "http://localhost:9000/state?x=1",
		"fragment smuggling":    "http://localhost:9000/state#x",
		"http off loopback":     "http://minio.internal:9000/state",
		"http private LAN":      "http://192.168.1.10:9000/state",
		"single-label metadata": "http://instance-data/latest/",
		"empty host":            "https:///state",
		"credential query":      "http://localhost:9000/state?token=abc",
		"at-sign in path":       "http://localhost:9000/a@b",
	}
	for name, value := range rejected {
		getenv := func(string) string { return value }
		if base, err := ResolveS3Endpoint(getenv); err == nil {
			t.Errorf("ResolveS3Endpoint(%q) = %q, want rejection (%s)", value, base, name)
		} else if strings.Contains(err.Error(), value) && strings.Contains(value, "secret") {
			t.Errorf("ResolveS3Endpoint(%q) error echoes credentials: %v", value, err)
		}
	}
}

// TestResolveS3EndpointAcceptsSingleLabelInClusterHTTP proves the tier2
// harness shape stays green: http to a single-label compose/cluster DNS
// name (http://minio:9000) resolves only inside the operator's own network,
// so the S3 surface accepts it while STS and dotted/private http stay shut.
func TestResolveS3EndpointAcceptsSingleLabelInClusterHTTP(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"http://minio:9000",
		"http://minio:9000/state/",
	} {
		getenv := func(string) string { return value }
		if _, err := ResolveS3Endpoint(getenv); err != nil {
			t.Errorf("ResolveS3Endpoint(%q) error = %v, want acceptance", value, err)
		}
	}
}

// TestResolveS3EndpointAcceptsOperatorShapes proves the shapes operators
// actually run stay green: https anywhere, http on loopback, hostnames,
// ports, and base-path prefixes.
func TestResolveS3EndpointAcceptsOperatorShapes(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"https://minio.internal:9000",
		"https://s3.us-east-1.amazonaws.com",
		"http://localhost:9000",
		"http://127.0.0.1:9000/state/",
		"http://[::1]:9000/",
		"HTTPS://MINIO.INTERNAL:9000/prefix",
	}
	for _, value := range accepted {
		getenv := func(string) string { return value }
		if _, err := ResolveS3Endpoint(getenv); err != nil {
			t.Errorf("ResolveS3Endpoint(%q) error = %v, want acceptance", value, err)
		}
	}
}

// TestNewHTTPS3ReaderCustomEndpointPathStyle proves the operator surface
// addresses MinIO path-style: base/bucket/key for GET and base/bucket for
// ListObjectsV2, every request SigV4-signed with ambient credentials.
func TestNewHTTPS3ReaderCustomEndpointPathStyle(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") {
			t.Errorf("Authorization = %q, want the AKID SigV4 scope", request.Header.Get("Authorization"))
		}
		if request.URL.Query().Get("list-type") == "2" {
			if request.URL.Path != "/state-bucket" {
				t.Errorf("list path = %q, want /state-bucket", request.URL.Path)
			}
			writer.Header().Set("Content-Type", "application/xml")
			_, _ = writer.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Contents><Key>env/terraform.tfstate</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`))
			return
		}
		if request.URL.Path != "/state-bucket/env/terraform.tfstate" {
			t.Errorf("GET path = %q, want /state-bucket/env/terraform.tfstate", request.URL.Path)
		}
		writer.Header().Set("ETag", `"minio-etag"`)
		_, _ = writer.Write([]byte(s3ObjectFixture))
	}))
	defer server.Close()

	getenv := func(key string) string {
		switch key {
		case S3EndpointEnv:
			return server.URL + "/"
		case "AWS_ACCESS_KEY_ID":
			return "AKID"
		case "AWS_SECRET_ACCESS_KEY":
			return "SECRET"
		}
		return ""
	}
	reader, err := NewHTTPS3Reader(getenv, nil)
	if err != nil {
		t.Fatalf("NewHTTPS3Reader error = %v", err)
	}
	object, err := reader.GetObject(context.Background(), "state-bucket", "env/terraform.tfstate", "us-east-1")
	if err != nil {
		t.Fatalf("GetObject error = %v", err)
	}
	if string(object.Body) != s3ObjectFixture || object.ETag != "minio-etag" {
		t.Fatalf("object = %+v, want the fixture body and minio-etag", object)
	}
	keys, err := reader.ListKeys(context.Background(), "state-bucket", "us-east-1", "")
	if err != nil {
		t.Fatalf("ListKeys error = %v", err)
	}
	if len(keys) != 1 || keys[0] != "env/terraform.tfstate" {
		t.Fatalf("keys = %q, want the one exact key", keys)
	}
}

// TestNewHTTPS3ReaderInvalidEndpointFailsClosed proves a misconfigured
// operator endpoint surfaces at construction instead of misdirecting
// signed reads at claim time.
func TestNewHTTPS3ReaderInvalidEndpointFailsClosed(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		if key == S3EndpointEnv {
			return "http://169.254.169.254/latest"
		}
		return ""
	}
	if _, err := NewHTTPS3Reader(getenv, nil); err == nil {
		t.Fatal("NewHTTPS3Reader(metadata endpoint) = nil, want fail-closed")
	}
}

// TestNewHTTPS3ReaderDefaultKeepsVirtualHosted proves an unset endpoint
// preserves the established AWS addressing: virtual-hosted buckets, with
// dotted buckets falling back to path style so TLS never breaks.
func TestNewHTTPS3ReaderDefaultKeepsVirtualHosted(t *testing.T) {
	t.Parallel()

	reader, err := NewHTTPS3Reader(func(string) string { return "" }, nil)
	if err != nil {
		t.Fatalf("NewHTTPS3Reader error = %v", err)
	}
	if got := reader.s3endpoint("plain-bucket", "us-east-1"); got != "https://plain-bucket.s3.us-east-1.amazonaws.com" {
		t.Fatalf("s3endpoint(plain) = %q, want virtual-hosted", got)
	}
	if got := reader.s3endpoint("dotted.bucket", "us-east-1"); got != "https://s3.us-east-1.amazonaws.com/dotted.bucket" {
		t.Fatalf("s3endpoint(dotted) = %q, want path-style", got)
	}
}

// TestSTSEndpointTrustParity proves AWS_STS_ENDPOINT carries the same
// trust gate as the S3 surface: the metadata service is refused while a
// loopback httptest endpoint (the offline/IRSA-test shape) still works.
// TestSTSEndpointRejectsSingleLabelHTTP proves the STS surface stays
// strict where the S3 surface allows in-cluster http: the web-identity
// exchange carries a bearer token, so http://minio:9000 fails closed for
// STS even though the S3 reader accepts it.
func TestSTSEndpointRejectsSingleLabelHTTP(t *testing.T) {
	t.Parallel()

	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("token-123\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile error = %v", err)
	}
	env := map[string]string{
		"AWS_ROLE_ARN":                "arn:aws:iam::123:role/eshu",
		"AWS_WEB_IDENTITY_TOKEN_FILE": tokenFile,
		"AWS_STS_ENDPOINT":            "http://minio:9000/",
	}
	provider := &credentialProvider{getenv: func(key string) string { return env[key] }}
	if _, err := provider.forRegion("us-east-1"); err == nil || !strings.Contains(err.Error(), "AWS_STS_ENDPOINT") {
		t.Fatalf("forRegion(single-label http STS) error = %v, want the endpoint trust failure", err)
	}
}

func TestSTSEndpointTrustParity(t *testing.T) {
	t.Parallel()

	evilToken := t.TempDir() + "/evil-token"
	if err := os.WriteFile(evilToken, []byte("token-123\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile error = %v", err)
	}
	evilEnv := map[string]string{
		"AWS_ROLE_ARN":                "arn:aws:iam::123:role/eshu",
		"AWS_WEB_IDENTITY_TOKEN_FILE": evilToken,
		"AWS_STS_ENDPOINT":            "http://169.254.169.254/latest/meta-data/",
	}
	evil := &credentialProvider{getenv: func(key string) string { return evilEnv[key] }}
	if _, err := evil.forRegion("us-east-1"); err == nil || !strings.Contains(err.Error(), "AWS_STS_ENDPOINT") {
		t.Fatalf("forRegion(metadata STS) error = %v, want the endpoint trust failure", err)
	}

	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("token-123\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/xml")
		_, _ = writer.Write([]byte(`<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>ASIAX</AccessKeyId><SecretAccessKey>shhh</SecretAccessKey><SessionToken>sess</SessionToken><Expiration>2030-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleWithWebIdentityResult></AssumeRoleWithWebIdentityResponse>`))
	}))
	defer server.Close()
	env := map[string]string{
		"AWS_ROLE_ARN":                "arn:aws:iam::123:role/eshu",
		"AWS_WEB_IDENTITY_TOKEN_FILE": tokenFile,
		"AWS_STS_ENDPOINT":            server.URL + "/",
	}
	provider := &credentialProvider{getenv: func(key string) string { return env[key] }}
	credentials, err := provider.forRegion("us-east-1")
	if err != nil {
		t.Fatalf("forRegion(loopback STS) error = %v, want the STS triple", err)
	}
	if credentials.AccessKey != "ASIAX" {
		t.Fatalf("credentials = %+v, want the STS triple", credentials)
	}
}
