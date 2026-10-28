// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// S3EndpointEnv names the operator S3-endpoint override. Set it to an
// S3-compatible base URL (MinIO, an offline mirror, a VPC endpoint) and
// every live S3 read addresses that endpoint path-style instead of the
// regional AWS endpoints. Unset or blank keeps the AWS default. It is the
// S3 counterpart of AWS_STS_ENDPOINT (see s3auth.go): both carry the same
// endpoint-trust gate below.
const S3EndpointEnv = "AWS_S3_ENDPOINT"

// metadataHosts are non-IP names that resolve to instance metadata on the
// common clouds. They are never trusted as an operator endpoint, whatever
// the scheme: a custom endpoint that reaches metadata answers with
// credentials, which is exactly the SSRF shape this gate exists to refuse.
var metadataHosts = map[string]struct{}{
	"metadata.google.internal": {},
	"instance-data":            {},
}

// ResolveS3Endpoint trims and trust-checks the operator S3 endpoint from
// the environment. It returns "" (no error) when the variable is unset or
// blank, selecting the regional AWS endpoints. A present-but-untrusted
// value fails closed: the caller must refuse to read rather than sign
// requests toward it.
func ResolveS3Endpoint(getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := strings.TrimSpace(getenv(S3EndpointEnv))
	if raw == "" {
		return "", nil
	}
	return validateEndpointTrust(raw)
}

// validateEndpointTrust enforces the operator endpoint posture shared by
// the S3 surface (AWS_S3_ENDPOINT) and STS (AWS_STS_ENDPOINT):
//   - scheme is exactly http or https (case-insensitive; anything else,
//     including a missing scheme, is refused);
//   - no userinfo anywhere: the value must not contain "@", so no
//     user:secret@host shape can smuggle credentials into a signed URL;
//   - host is non-empty; query strings and fragments are refused, so the
//     value stays a base URL and cannot smuggle selectors or tokens;
//   - instance-metadata destinations are refused in every scheme: IPv4
//     link-local (169.254.0.0/16, the cloud metadata range), IPv6
//     link-local, and the well-known metadata hostnames;
//   - http is confined to loopback (localhost, 127.0.0.0/8, ::1) for
//     offline/MinIO development; every non-loopback host requires https,
//     so production traffic is never downgraded to cleartext by config.
//
// The returned base keeps any operator path prefix with cosmetic trailing
// slashes trimmed. Error text never echoes the value: an endpoint can name
// internal topology.
func validateEndpointTrust(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("endpoint must not be blank")
	}
	// url.Parse only surfaces userinfo after "//", so check "@" first: a
	// hierarchical userinfo (https://user:secret@host) and an opaque
	// credential body are both refused before parsing.
	if strings.Contains(trimmed, "@") {
		return "", fmt.Errorf("endpoint must not embed credentials")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("endpoint is not a valid URL")
	}
	switch scheme := strings.ToLower(parsed.Scheme); scheme {
	case "https":
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("http endpoints are allowed only on loopback")
		}
	default:
		return "", fmt.Errorf("endpoint scheme must be http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("endpoint must not embed credentials")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return "", fmt.Errorf("endpoint must name a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("endpoint must be a base URL without query or fragment")
	}
	if _, denied := metadataHosts[host]; denied {
		return "", fmt.Errorf("endpoint must not target instance metadata")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
		return "", fmt.Errorf("endpoint must not target instance metadata")
	}
	return strings.TrimRight(trimmed, "/"), nil
}

// isLoopbackHost reports whether a URL hostname is loopback: "localhost"
// or a loopback IP (127.0.0.0/8, ::1). Only loopback hosts may serve the
// http endpoint shape; everything else requires https.
func isLoopbackHost(host string) bool {
	trimmed := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if trimmed == "localhost" {
		return true
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// NewHTTPS3Reader builds the live-path S3 reader: ambient workload
// credentials, a bounded default transport unless httpClient is given, and
// the operator endpoint from AWS_S3_ENDPOINT when set. A custom endpoint is
// bound path-style (base/bucket/key), the addressing MinIO and most
// S3-compatible servers expect; the AWS default keeps virtual-hosted
// addressing with the dotted-bucket path-style fallback. An untrusted
// endpoint fails here, before any request is signed.
func NewHTTPS3Reader(getenv func(string) string, httpClient *http.Client) (*HTTPS3Reader, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	base, err := ResolveS3Endpoint(getenv)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", S3EndpointEnv, err)
	}
	reader := &HTTPS3Reader{Getenv: getenv, HTTPClient: httpClient}
	if base != "" {
		reader.endpoint = func(bucket, _ string) string { return base + "/" + bucket }
	}
	return reader, nil
}
