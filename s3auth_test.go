// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSignV4IsDeterministicAndBounded proves the signer emits the
// AWS4-HMAC-SHA256 shape with the exact credential scope and a 64-hex
// signature, deterministically for fixed inputs.
func TestSignV4IsDeterministicAndBounded(t *testing.T) {
	t.Parallel()

	credentials := awsCredentials{AccessKey: "AKID", SecretKey: "SECRET", SessionToken: "TOKEN"}
	build := func() *http.Request {
		request, err := http.NewRequest(http.MethodGet, "https://b.s3.us-east-1.amazonaws.com/k?list-type=2", nil)
		if err != nil {
			t.Fatalf("http.NewRequest error = %v", err)
		}
		signV4(request, credentials, "us-east-1", "s3", "UNSIGNED-PAYLOAD", time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC))
		return request
	}
	first, second := build(), build()
	if first.Header.Get("Authorization") != second.Header.Get("Authorization") {
		t.Fatal("signV4 is not deterministic for fixed inputs")
	}
	header := first.Header.Get("Authorization")
	if !strings.HasPrefix(header, "AWS4-HMAC-SHA256 Credential=AKID/20261027/us-east-1/s3/aws4_request, SignedHeaders=") {
		t.Fatalf("Authorization = %q, want the AKID scope prefix", header)
	}
	if !strings.Contains(header, "Signature=") || len(header[strings.LastIndex(header, "Signature=")+len("Signature="):]) != 64 {
		t.Fatalf("Authorization = %q, want a 64-hex signature", header)
	}
	signed := first.Header.Get("X-Amz-Security-Token")
	if signed != "TOKEN" {
		t.Fatalf("X-Amz-Security-Token = %q, want the session token", signed)
	}
}

// TestWebIdentityExchangeParsesSTSCredentials proves the IRSA chain: role
// ARN plus a projected token file exchanges at STS for short-lived
// credentials, with no static secret in the environment.
func TestWebIdentityExchangeParsesSTSCredentials(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Errorf("ParseForm error = %v", err)
		}
		if request.Form.Get("Action") != "AssumeRoleWithWebIdentity" {
			t.Errorf("Action = %q, want AssumeRoleWithWebIdentity", request.Form.Get("Action"))
		}
		if request.Form.Get("WebIdentityToken") != "token-123" {
			t.Errorf("WebIdentityToken = %q, want the projected token", request.Form.Get("WebIdentityToken"))
		}
		writer.Header().Set("Content-Type", "text/xml")
		_, _ = writer.Write([]byte(`<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>ASIAX</AccessKeyId><SecretAccessKey>shhh</SecretAccessKey><SessionToken>sess</SessionToken><Expiration>2030-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleWithWebIdentityResult><ResponseMetadata><RequestId>r</RequestId></ResponseMetadata></AssumeRoleWithWebIdentityResponse>`))
	}))
	defer server.Close()

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("token-123\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile error = %v", err)
	}
	env := map[string]string{
		"AWS_ROLE_ARN":                "arn:aws:iam::123:role/eshu",
		"AWS_WEB_IDENTITY_TOKEN_FILE": tokenFile,
		"AWS_STS_ENDPOINT":            server.URL,
	}
	provider := &credentialProvider{getenv: func(key string) string { return env[key] }}
	credentials, err := provider.forRegion("us-east-1")
	if err != nil {
		t.Fatalf("forRegion error = %v", err)
	}
	if credentials.AccessKey != "ASIAX" || credentials.SecretKey != "shhh" || credentials.SessionToken != "sess" {
		t.Fatalf("credentials = %+v, want the STS triple", credentials)
	}
	if !credentials.valid(time.Now().UTC()) {
		t.Fatal("credentials.valid = false, want true before the STS expiry")
	}
}

// TestAmbientCredentialChainPrefersWebIdentity proves IRSA wins over the
// environment triple and that an empty environment fails closed with
// guidance instead of anonymous requests.
func TestAmbientCredentialChainPrefersWebIdentity(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AWS_ROLE_ARN":                "arn:aws:iam::123:role/eshu",
		"AWS_WEB_IDENTITY_TOKEN_FILE": filepath.Join(t.TempDir(), "missing-token"),
		"AWS_ACCESS_KEY_ID":           "AKID",
		"AWS_SECRET_ACCESS_KEY":       "SECRET",
	}
	provider := &credentialProvider{getenv: func(key string) string { return env[key] }}
	if _, err := provider.forRegion("us-east-1"); err == nil || !strings.Contains(err.Error(), "web identity token file") {
		t.Fatalf("forRegion(broken token file) error = %v, want the token read failure", err)
	}
	empty := &credentialProvider{getenv: func(string) string { return "" }}
	if _, err := empty.forRegion("us-east-1"); err == nil || !strings.Contains(err.Error(), "no ambient AWS credentials") {
		t.Fatalf("forRegion(empty env) error = %v, want the no-credentials guidance", err)
	}
}
