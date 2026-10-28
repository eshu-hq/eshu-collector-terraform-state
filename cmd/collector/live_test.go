// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	collector "github.com/eshu-hq/eshu-collector-terraform-state"
	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

// TestParseSDKStdioLiveConfigWithoutSourceInput proves a host-shaped
// activation request carrying discovery/target_scopes (and no source.input
// file) parses into a live plan instead of failing with a missing
// source.input error. This is the ops-qa shape: an s3 backend filter plus
// target scopes, resolved and fetched at claim time.
func TestParseSDKStdioLiveConfigWithoutSourceInput(t *testing.T) {
	t.Parallel()

	request := sdkRequest{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Claim:           sdk.Claim{Scope: sdk.Scope{ID: "component:terraform-state-primary", Kind: "component"}},
		Config: map[string]any{
			"discovery": map[string]any{
				"backend_filters": []any{
					map[string]any{
						"target_scope_id": "ops-qa-aws",
						"backend_kind":    "s3",
						"bucket":          "bg-ops-qa-terraform-state",
						"region":          "us-east-1",
					},
				},
			},
			"target_scopes": []any{
				map[string]any{
					"target_scope_id":  "ops-qa-aws",
					"provider":         "aws",
					"deployment_mode":  "account_local",
					"allowed_regions":  []any{"us-east-1"},
					"allowed_backends": []any{"s3"},
				},
			},
			"limits": map[string]any{"claimTimeoutSeconds": 60.0},
		},
	}
	var stdin bytes.Buffer
	if err := json.NewEncoder(&stdin).Encode(request); err != nil {
		t.Fatalf("json.Encode(request) error = %v", err)
	}
	plan, err := parseSDKStdio(&stdin)
	if err != nil {
		t.Fatalf("parseSDKStdio(live config) error = %v, want a live plan without source.input", err)
	}
	if plan.inputPath != "" {
		t.Fatalf("plan.inputPath = %q, want empty for a live plan", plan.inputPath)
	}
	if !plan.live {
		t.Fatal("plan.live = false, want true for a discovery/target_scopes request")
	}
}

// stubLiveReader serves one canned state object and listing without a
// network so the stdio live path proves out end to end.
type stubLiveReader struct {
	body []byte
}

func (stub stubLiveReader) GetObject(_ context.Context, _, _, _ string) (collector.StateObject, error) {
	return collector.StateObject{Body: stub.body}, nil
}

func (stub stubLiveReader) ListKeys(_ context.Context, _, _, _ string) ([]string, error) {
	return []string{"env/terraform.tfstate"}, nil
}

// TestRunSDKStdioLiveClaimCollects proves the ops-qa host shape collects
// end to end: discovery plus target scopes with no source.input exits 0
// and emits a complete result instead of the old exit-1 missing-input
// failure. It is not parallel: it swaps the process-wide reader factory.
func TestRunSDKStdioLiveClaimCollects(t *testing.T) {
	previous := liveReaderFactory
	liveReaderFactory = func() (collector.SourceReader, error) {
		return stubLiveReader{body: []byte(`{"resources": [{"mode": "managed", "type": "aws_s3_bucket", "name": "one", "instances": [{}]}]}`)}, nil
	}
	defer func() { liveReaderFactory = previous }()

	request := sdkRequest{
		ProtocolVersion: sdk.ProtocolVersionV1Alpha1,
		Claim:           sdk.Claim{Scope: sdk.Scope{ID: "component:terraform-state-primary", Kind: "component"}},
		Config: map[string]any{
			"discovery": map[string]any{
				"backend_filters": []any{
					map[string]any{
						"target_scope_id": "ops-qa-aws",
						"backend_kind":    "s3",
						"bucket":          "bg-ops-qa-terraform-state",
						"region":          "us-east-1",
					},
				},
			},
			"target_scopes": []any{
				map[string]any{
					"target_scope_id":  "ops-qa-aws",
					"provider":         "aws",
					"deployment_mode":  "account_local",
					"allowed_regions":  []any{"us-east-1"},
					"allowed_backends": []any{"s3"},
				},
			},
			"limits": map[string]any{"claimTimeoutSeconds": 60.0},
		},
	}
	var stdin bytes.Buffer
	if err := json.NewEncoder(&stdin).Encode(request); err != nil {
		t.Fatalf("json.Encode(request) error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--sdk-stdio"}, &stdin, &stdout, &stderr); err != nil {
		t.Fatalf("run(--sdk-stdio live) error = %v, want exit 0", err)
	}
	var result sdk.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal(result) error = %v", err)
	}
	if result.State != sdk.ResultComplete {
		t.Fatalf("State = %q, want complete for the live fixture run", result.State)
	}
	if len(result.Facts) != 2 {
		t.Fatalf("facts = %d, want one resource plus one snapshot", len(result.Facts))
	}
	if !strings.Contains(stderr.String(), `"claim_timeout_seconds":60`) {
		t.Fatalf("health snapshot missing the request timeout: %q", stderr.String())
	}
}
