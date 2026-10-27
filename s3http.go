// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"context"
	"encoding/xml"
	"errors"
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

// Sentinel errors classifying one S3 state read. They mirror the in-tree
// source sentinels (ErrStateNotModified, ErrStateMissing) plus the
// transient class the satellite maps to a retryable claim result.
var (
	// ErrStateMissing reports an exact configured or discovered state
	// object that no longer exists at read time.
	ErrStateMissing = errors.New("terraform state source is missing")
	// ErrStateTooLarge reports a state object past the read ceiling.
	ErrStateTooLarge = errors.New("terraform state source exceeds the read ceiling")
	// ErrStateTransient reports a throttled or server-side failure the
	// host should retry with a bounded backoff.
	ErrStateTransient = errors.New("terraform state source is temporarily unavailable")
)

// maxLiveStateBytes caps one fetched state document. It matches the
// placeholder LoadReport bound so live and file inputs share one ceiling.
const maxLiveStateBytes int64 = 32 << 20

// StateObject is one fetched state document plus its S3 ETag.
type StateObject struct {
	Body []byte
	ETag string
}

// SourceReader opens exact S3 state objects and lists bucket prefixes. The
// production implementation signs every request with ambient workload
// credentials; tests substitute a fake.
type SourceReader interface {
	GetObject(ctx context.Context, bucket, key, region string) (StateObject, error)
	ListKeys(ctx context.Context, bucket, region, prefix string) ([]string, error)
}

// HTTPS3Reader is the stdlib-only S3 reader: SigV4-signed HTTPS against
// the regional endpoints, no SDK dependency. Reads are read-only exact-key
// GETs plus prefix listings; it never writes, never deletes, and never
// carries credential material in errors.
type HTTPS3Reader struct {
	// Getenv resolves ambient credential environment; nil reads the
	// process environment.
	Getenv func(string) string
	// HTTPClient performs signed requests; nil selects a bounded default.
	HTTPClient *http.Client
	// endpoint overrides the S3 base URL for tests (httptest servers);
	// nil selects the regional S3 endpoints.
	endpoint func(bucket, region string) string

	mu       sync.Mutex
	provider *credentialProvider
}

// s3endpoint resolves the request base URL for one bucket and region.
func (reader *HTTPS3Reader) s3endpoint(bucket, region string) string {
	if reader.endpoint != nil {
		return strings.TrimSuffix(reader.endpoint(bucket, region), "/")
	}
	if strings.Contains(bucket, ".") {
		return "https://s3." + region + ".amazonaws.com/" + bucket
	}
	return "https://" + bucket + ".s3." + region + ".amazonaws.com"
}

func (reader *HTTPS3Reader) getenv(key string) string {
	if reader.Getenv != nil {
		return reader.Getenv(key)
	}
	return os.Getenv(key)
}

func (reader *HTTPS3Reader) client() *http.Client {
	if reader.HTTPClient != nil {
		return reader.HTTPClient
	}
	return defaultHTTPClient()
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func (reader *HTTPS3Reader) credentials(region string) (awsCredentials, error) {
	reader.mu.Lock()
	if reader.provider == nil {
		getenv := reader.getenv
		httpClient := reader.client()
		reader.provider = &credentialProvider{
			getenv: getenv,
			http:   func() *http.Client { return httpClient },
		}
	}
	provider := reader.provider
	reader.mu.Unlock()
	return provider.forRegion(region)
}

// s3RequestURL builds the object URL under the reader's endpoint.
// Virtual-hosted buckets address the key at the root; dot-bearing buckets
// fall back to path style at endpoint selection so a dotted host never
// breaks TLS. Query parameters carry listing selectors.
func (reader *HTTPS3Reader) s3RequestURL(bucket, key, region string, query url.Values) string {
	escaped := strings.TrimPrefix(canonicalV4URI(&url.URL{Path: "/" + key}), "/")
	target := reader.s3endpoint(bucket, region) + "/" + escaped
	if len(query) > 0 {
		return target + "?" + encodeV4Query(query)
	}
	return target
}

// encodeV4Query serializes listing selectors with RFC 3986 escaping.
// url.Values.Encode would emit "+" for spaces, which SigV4 does not accept:
// the wire bytes and the canonical query must agree exactly.
func encodeV4Query(query url.Values) string {
	names := make([]string, 0, len(query))
	for name := range query {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0)
	for _, name := range names {
		entries := append([]string{}, query[name]...)
		sort.Strings(entries)
		for _, entry := range entries {
			pairs = append(pairs, v4Escape(name)+"="+v4Escape(entry))
		}
	}
	return strings.Join(pairs, "&")
}

// GetObject reads one exact S3 object, enforcing the read ceiling while
// streaming. A missing key maps to ErrStateMissing; throttles and
// server-side failures map to ErrStateTransient; anything else (including
// auth) fails closed with bucket and key redacted out of the message.
func (reader *HTTPS3Reader) GetObject(ctx context.Context, bucket, key, region string) (StateObject, error) {
	bucket = strings.TrimSpace(bucket)
	key = strings.Trim(strings.TrimSpace(key), "/")
	region = strings.ToLower(strings.TrimSpace(region))
	if bucket == "" || key == "" || region == "" {
		return StateObject{}, fmt.Errorf("s3 state bucket, key, and region must not be blank")
	}
	credentials, err := reader.credentials(region)
	if err != nil {
		return StateObject{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, reader.s3RequestURL(bucket, key, region, nil), nil)
	if err != nil {
		return StateObject{}, redactS3Locator(fmt.Errorf("build s3 request: %w", err), bucket, key)
	}
	signV4(request, credentials, region, "s3", "UNSIGNED-PAYLOAD", time.Now().UTC())
	response, err := reader.client().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return StateObject{}, ctx.Err()
		}
		return StateObject{}, redactS3Locator(fmt.Errorf("s3 read: %w", err), bucket, key)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
		// Continue below.
	case http.StatusNotFound:
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return StateObject{}, s3NotFoundError(raw, bucket, key)
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return StateObject{}, errors.Join(ErrStateTransient, redactS3Locator(s3CodeError(response, "transient"), bucket, key))
	default:
		return StateObject{}, redactS3Locator(s3CodeError(response, "unexpected status"), bucket, key)
	}
	if response.ContentLength > maxLiveStateBytes {
		return StateObject{}, fmt.Errorf("%w in region %q", ErrStateTooLarge, region)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxLiveStateBytes+1))
	if err != nil {
		return StateObject{}, redactS3Locator(fmt.Errorf("s3 read body: %w", err), bucket, key)
	}
	if int64(len(raw)) > maxLiveStateBytes {
		return StateObject{}, fmt.Errorf("%w in region %q", ErrStateTooLarge, region)
	}
	return StateObject{Body: raw, ETag: strings.Trim(response.Header.Get("ETag"), `"`)}, nil
}

// ListKeys returns the object keys under one bucket prefix via ListObjectsV2,
// following continuation tokens. Keys that name no exact object (trailing
// "/") are skipped; callers cap the resulting candidate set.
func (reader *HTTPS3Reader) ListKeys(ctx context.Context, bucket, region, prefix string) ([]string, error) {
	bucket = strings.TrimSpace(bucket)
	region = strings.ToLower(strings.TrimSpace(region))
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if bucket == "" || region == "" {
		return nil, fmt.Errorf("s3 list bucket and region must not be blank")
	}
	credentials, err := reader.credentials(region)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	continuation := ""
	for pages := 0; ; pages++ {
		if pages >= 10 {
			return nil, fmt.Errorf("s3 listing in region %q exceeds the page bound", region)
		}
		query := url.Values{}
		query.Set("list-type", "2")
		query.Set("max-keys", "1000")
		if prefix != "" {
			query.Set("prefix", prefix)
		}
		if continuation != "" {
			query.Set("continuation-token", continuation)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, reader.s3RequestURL(bucket, "", region, query), nil)
		if err != nil {
			return nil, redactS3Locator(fmt.Errorf("build s3 list request: %w", err), bucket, prefix)
		}
		signV4(request, credentials, region, "s3", "UNSIGNED-PAYLOAD", time.Now().UTC())
		response, err := reader.client().Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, redactS3Locator(fmt.Errorf("s3 list: %w", err), bucket, prefix)
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if err != nil {
			return nil, redactS3Locator(fmt.Errorf("s3 list body: %w", err), bucket, prefix)
		}
		switch response.StatusCode {
		case http.StatusOK:
			// Continue below.
		case http.StatusNotFound:
			return nil, s3NotFoundError(raw, bucket, prefix)
		case http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return nil, errors.Join(ErrStateTransient, redactS3Locator(s3CodeErrorBody(raw, "transient"), bucket, prefix))
		default:
			return nil, redactS3Locator(s3CodeErrorBody(raw, "unexpected status"), bucket, prefix)
		}
		var listing listBucketResult
		if err := xml.Unmarshal(raw, &listing); err != nil {
			return nil, redactS3Locator(fmt.Errorf("decode s3 listing: %w", err), bucket, prefix)
		}
		for _, content := range listing.Contents {
			key := strings.TrimSpace(content.Key)
			if key == "" || strings.HasSuffix(key, "/") {
				continue
			}
			keys = append(keys, key)
		}
		if !listing.IsTruncated {
			return keys, nil
		}
		continuation = strings.TrimSpace(listing.NextContinuationToken)
		if continuation == "" {
			return keys, nil
		}
	}
}

type listBucketResult struct {
	XMLName               xml.Name            `xml:"ListBucketResult"`
	Contents              []listBucketContent `xml:"Contents"`
	IsTruncated           bool                `xml:"IsTruncated"`
	NextContinuationToken string              `xml:"NextContinuationToken"`
}

type listBucketContent struct {
	Key string `xml:"Key"`
}

// s3NotFoundError classifies one S3 404 body. A NoSuchBucket names a wrong
// bucket configuration and fails closed as a hard error; every other 404
// (NoSuchKey, NoSuchVersion, unparseable) is a missing object.
func s3NotFoundError(raw []byte, bucket, key string) error {
	var fault s3ErrorBody
	if err := xml.Unmarshal(raw, &fault); err == nil && strings.TrimSpace(fault.Code) == "NoSuchBucket" {
		return redactS3Locator(fmt.Errorf("s3 bucket does not exist (check discovery bucket)"), bucket, key)
	}
	return errors.Join(ErrStateMissing, redactS3Locator(fmt.Errorf("s3 object not found"), bucket, key))
}

// s3CodeError drains a short error body and reports the S3 error code when
// the body parses, so operators can tell SlowDown from AccessDenied.
func s3CodeError(response *http.Response, fallback string) error {
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("s3 status %d: %s", response.StatusCode, fallback)
	}
	return s3CodeErrorBody(raw, fallback, response.StatusCode)
}

func s3CodeErrorBody(raw []byte, fallback string, status ...int) error {
	var fault s3ErrorBody
	if err := xml.Unmarshal(raw, &fault); err == nil && strings.TrimSpace(fault.Code) != "" {
		code := strings.TrimSpace(fault.Code)
		if code == "NoSuchBucket" {
			return fmt.Errorf("s3 bucket does not exist (check discovery bucket)")
		}
		if code == "NoSuchKey" || code == "NoSuchVersion" {
			if len(status) > 0 && status[0] == http.StatusNotFound {
				return fmt.Errorf("s3 object not found")
			}
			return fmt.Errorf("s3 error %s", code)
		}
		if len(status) > 0 {
			return fmt.Errorf("s3 status %d: %s", status[0], code)
		}
		return fmt.Errorf("s3 error %s", code)
	}
	if len(status) > 0 {
		return fmt.Errorf("s3 status %d: %s", status[0], fallback)
	}
	return fmt.Errorf("s3 error: %s", fallback)
}

type s3ErrorBody struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// redactS3Locator mirrors the in-tree safeError: bucket and key material
// never reaches an operator-facing error, while region (routing, not
// identity) stays for debuggability.
func redactS3Locator(err error, bucket, key string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, value := range []string{key, bucket} {
		if strings.TrimSpace(value) != "" {
			message = strings.ReplaceAll(message, value, "<redacted>")
		}
	}
	return errors.New(message)
}
