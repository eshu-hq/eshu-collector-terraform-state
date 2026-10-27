// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// awsCredentials are short-lived ambient workload credentials. They never
// come from claim config: the chain reads only the standard ambient
// environment (IRSA/EKS web identity first, then the process environment
// triple), so no static credential and no credential key ever enters code
// or config.
type awsCredentials struct {
	AccessKey    string
	SecretKey    string
	SessionToken string
	ExpiresAt    time.Time
}

func (credentials awsCredentials) valid(now time.Time) bool {
	if credentials.AccessKey == "" || credentials.SecretKey == "" {
		return false
	}
	if credentials.ExpiresAt.IsZero() {
		return true
	}
	return now.Add(30 * time.Second).Before(credentials.ExpiresAt)
}

// credentialProvider resolves ambient AWS credentials with an IRSA-first
// chain. It is safe for concurrent use; STS results cache until expiry.
type credentialProvider struct {
	getenv func(string) string
	http   func() *http.Client

	mu     sync.Mutex
	cached awsCredentials
}

// httpClient resolves the STS transport, defaulting to the bounded client
// when the provider carries no override (including test-only providers).
func (provider *credentialProvider) httpClient() *http.Client {
	if provider.http != nil {
		if client := provider.http(); client != nil {
			return client
		}
	}
	return defaultHTTPClient()
}

func (provider *credentialProvider) forRegion(region string) (awsCredentials, error) {
	now := time.Now().UTC()
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.cached.valid(now) {
		return provider.cached, nil
	}
	credentials, err := provider.resolveLocked(region)
	if err != nil {
		return awsCredentials{}, err
	}
	provider.cached = credentials
	return credentials, nil
}

func (provider *credentialProvider) resolveLocked(region string) (awsCredentials, error) {
	getenv := provider.getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	roleARN := strings.TrimSpace(getenv("AWS_ROLE_ARN"))
	tokenFile := strings.TrimSpace(getenv("AWS_WEB_IDENTITY_TOKEN_FILE"))
	if roleARN != "" && tokenFile != "" {
		credentials, err := provider.assumeRoleWithWebIdentity(roleARN, tokenFile, region)
		if err != nil {
			return awsCredentials{}, err
		}
		return credentials, nil
	}
	accessKey := strings.TrimSpace(getenv("AWS_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(getenv("AWS_SECRET_ACCESS_KEY"))
	if accessKey != "" && secretKey != "" {
		return awsCredentials{
			AccessKey:    accessKey,
			SecretKey:    secretKey,
			SessionToken: strings.TrimSpace(getenv("AWS_SESSION_TOKEN")),
		}, nil
	}
	return awsCredentials{}, fmt.Errorf("no ambient AWS credentials: set IRSA web identity (AWS_ROLE_ARN plus AWS_WEB_IDENTITY_TOKEN_FILE) or the process environment credential triple")
}

// assumeRoleWithWebIdentity exchanges the IRSA projected token for
// short-lived STS credentials. The STS request is unsigned: the web
// identity token is the bearer. It must run while provider.mu is held.
func (provider *credentialProvider) assumeRoleWithWebIdentity(roleARN, tokenFile, region string) (awsCredentials, error) {
	getenv := provider.getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return awsCredentials{}, fmt.Errorf("read web identity token file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return awsCredentials{}, fmt.Errorf("web identity token file is empty")
	}
	stsRegion := strings.TrimSpace(region)
	if stsRegion == "" {
		stsRegion = "us-east-1"
	}
	form := url.Values{}
	form.Set("Action", "AssumeRoleWithWebIdentity")
	form.Set("Version", "2011-06-15")
	form.Set("RoleArn", roleARN)
	form.Set("RoleSessionName", "eshu-collector-terraform-state")
	form.Set("WebIdentityToken", token)
	body := form.Encode()
	// AWS_STS_ENDPOINT overrides the STS endpoint for VPC endpoints and
	// offline tests; otherwise the regional endpoint serves.
	endpoint := strings.TrimSpace(getenv("AWS_STS_ENDPOINT"))
	if endpoint == "" {
		endpoint = "https://sts." + stsRegion + ".amazonaws.com/"
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return awsCredentials{}, fmt.Errorf("build STS request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	payloadHash := sha256Hex([]byte(body))
	// The STS call carries no long-lived secret, but it is still signed
	// with the empty credential shape so the request has a fixed form.
	now := time.Now().UTC()
	signV4(request, awsCredentials{}, stsRegion, "sts", payloadHash, now)
	client := provider.httpClient()
	response, err := client.Do(request)
	if err != nil {
		return awsCredentials{}, fmt.Errorf("STS AssumeRoleWithWebIdentity: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return awsCredentials{}, fmt.Errorf("STS AssumeRoleWithWebIdentity: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return awsCredentials{}, fmt.Errorf("STS AssumeRoleWithWebIdentity: unexpected status %d", response.StatusCode)
	}
	var envelope stsAssumeRoleResponse
	if err := xml.Unmarshal(responseBody, &envelope); err != nil {
		return awsCredentials{}, fmt.Errorf("decode STS credentials: %w", err)
	}
	credentials := envelope.Result.Credentials
	if strings.TrimSpace(credentials.AccessKeyID) == "" || strings.TrimSpace(credentials.SecretAccessKey) == "" {
		return awsCredentials{}, fmt.Errorf("STS returned incomplete credentials")
	}
	var expiresAt time.Time
	if trimmed := strings.TrimSpace(credentials.Expiration); trimmed != "" {
		if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
			expiresAt = parsed.UTC()
		}
	}
	return awsCredentials{
		AccessKey:    strings.TrimSpace(credentials.AccessKeyID),
		SecretKey:    strings.TrimSpace(credentials.SecretAccessKey),
		SessionToken: strings.TrimSpace(credentials.SessionToken),
		ExpiresAt:    expiresAt,
	}, nil
}

type stsAssumeRoleResponse struct {
	XMLName xml.Name            `xml:"AssumeRoleWithWebIdentityResponse"`
	Result  stsAssumeRoleResult `xml:"AssumeRoleWithWebIdentityResult"`
}

type stsAssumeRoleResult struct {
	Credentials stsCredentials `xml:"Credentials"`
}

type stsCredentials struct {
	AccessKeyID     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	SessionToken    string `xml:"SessionToken"`
	Expiration      string `xml:"Expiration"`
}

// signV4 applies AWS Signature Version 4 to an outbound request. It signs
// the exact bytes on the wire (method, escaped path, sorted query,
// signed headers) with the standard AWS4-HMAC-SHA256 chain.
func signV4(request *http.Request, credentials awsCredentials, region, service, payloadHash string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	request.Header.Set("X-Amz-Date", amzDate)
	if service == "s3" && payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if strings.TrimSpace(credentials.SessionToken) != "" {
		request.Header.Set("X-Amz-Security-Token", credentials.SessionToken)
	}
	// STS web-identity exchange is unsigned: the token is the bearer, and
	// a credential-less Authorization line would only confuse the
	// endpoint, so it is omitted when there is no access key.
	if strings.TrimSpace(credentials.AccessKey) == "" {
		return
	}
	signedHeaders, canonicalHeaders := canonicalV4Headers(request)
	canonicalRequest := strings.Join([]string{
		request.Method,
		canonicalV4URI(request.URL),
		canonicalV4Query(request.URL),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := v4SigningKey(credentials.SecretKey, dateStamp, region, service)
	signature := hex.EncodeToString(v4HMAC(signingKey, stringToSign))
	credentialScope := credentials.AccessKey + "/" + scope
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+credentialScope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func canonicalV4Headers(request *http.Request) (string, string) {
	headers := map[string]string{}
	for name, values := range request.Header {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "authorization" {
			continue
		}
		trimmed := make([]string, 0, len(values))
		for _, value := range values {
			trimmed = append(trimmed, strings.TrimSpace(value))
		}
		headers[lower] = strings.Join(trimmed, ",")
	}
	host := strings.TrimSpace(request.Host)
	if host == "" {
		host = strings.TrimSpace(request.URL.Host)
	}
	headers["host"] = host
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines strings.Builder
	for _, name := range names {
		lines.WriteString(name)
		lines.WriteString(":")
		lines.WriteString(headers[name])
		lines.WriteString("\n")
	}
	return strings.Join(names, ";"), lines.String()
}

func canonicalV4URI(urlValue *url.URL) string {
	rawPath := urlValue.EscapedPath()
	if rawPath == "" {
		return "/"
	}
	segments := strings.Split(rawPath, "/")
	for index, segment := range segments {
		unescaped, err := url.PathUnescape(segment)
		if err != nil {
			unescaped = segment
		}
		segments[index] = v4Escape(unescaped)
	}
	return strings.Join(segments, "/")
}

func canonicalV4Query(urlValue *url.URL) string {
	values := urlValue.Query()
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0)
	for _, name := range names {
		entries := values[name]
		sort.Strings(entries)
		for _, entry := range entries {
			pairs = append(pairs, v4Escape(name)+"="+v4Escape(entry))
		}
	}
	return strings.Join(pairs, "&")
}

// v4Escape applies RFC 3986 escaping with the SigV4 unreserved set
// (ALPHA / DIGIT / "-" / "." / "_" / "~").
func v4Escape(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' ||
			char == '-' || char == '_' || char == '.' || char == '~' {
			out.WriteByte(char)
			continue
		}
		fmt.Fprintf(&out, "%%%02X", char)
	}
	return out.String()
}

func v4SigningKey(secret, dateStamp, region, service string) []byte {
	kDate := v4HMAC([]byte("AWS4"+secret), dateStamp)
	kRegion := v4HMAC(kDate, region)
	kService := v4HMAC(kRegion, service)
	return v4HMAC(kService, "aws4_request")
}

func v4HMAC(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
