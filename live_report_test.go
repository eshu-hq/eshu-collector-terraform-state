// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/eshu-hq/eshu-sdk/collector"
)

const liveStateFixture = `{
	"resources": [
		{"mode": "managed", "type": "aws_s3_bucket", "name": "state", "instances": [{"attributes": {"bucket": "bg-ops-qa-terraform-state", "acl": "private", "secret_token": "hunter2"}}]},
		{"mode": "data", "type": "aws_ami", "name": "base", "module": "module.vpc", "instances": []},
		{"type": "", "name": "broken", "instances": []}
	],
	"outputs": {"db_password": {"value": "hunter3", "sensitive": true}}
}`

// TestReportForStateEmitsAllowlistOnly proves the state projection carries
// addresses, types, and modes plus only contracts-allowlisted attribute
// leaves (here: the s3 acl), while no secret or non-allowlisted value (the
// hunter2/hunter3/secret_token markers, the bucket name) reaches any record
// payload.
func TestReportForStateEmitsAllowlistOnly(t *testing.T) {
	t.Parallel()

	report, err := ReportForState("s3://b/k", []byte(liveStateFixture))
	if err != nil {
		t.Fatalf("ReportForState error = %v", err)
	}
	if len(report.Records) != 2 {
		t.Fatalf("records = %+v, want the two well-formed resources", report.Records)
	}
	encoded, err := json.Marshal(report.Records)
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}
	for _, secret := range []string{"hunter2", "hunter3", "db_password", "secret_token", "bg-ops-qa-terraform-state"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("record payloads leak %q: %s", secret, encoded)
		}
	}
	byID := map[string]Record{}
	for _, record := range report.Records {
		byID[record.ID] = record
	}
	managed := byID["aws_s3_bucket.state"]
	if managed.Name != "aws_s3_bucket" || managed.Detail != "managed" {
		t.Fatalf("managed record = %+v, want type name and managed detail", managed)
	}
	if acl, ok := managed.Attributes["acl"]; !ok || acl != "private" {
		t.Fatalf("managed attributes = %#v, want exactly the allowlisted acl", managed.Attributes)
	}
	if len(managed.Attributes) != 1 {
		t.Fatalf("managed attributes = %#v, want only the acl leaf", managed.Attributes)
	}
	data := byID["module.vpc.aws_ami.base"]
	if data.Name != "aws_ami" || data.Detail != "data" {
		t.Fatalf("data record = %+v, want module address and data detail", data)
	}
	if _, err := ReportForState("s3://b/k", []byte("{nope")); err == nil {
		t.Fatal("ReportForState(invalid JSON) error = nil, want failure")
	}
}

// TestReportForStateCapturesSerialAndLineage proves the state revision
// identity reaches the report: the host requires it in the generation
// freshness hint, and multi-state merges join per-state pairs.
func TestReportForStateCapturesSerialAndLineage(t *testing.T) {
	t.Parallel()

	report, err := ReportForState("s3://b/k", []byte(`{"serial": 17, "lineage": "lin-123", "resources": []}`))
	if err != nil {
		t.Fatalf("ReportForState error = %v", err)
	}
	if report.Serial != 17 || report.Lineage != "lin-123" {
		t.Fatalf("identity = (%d, %q), want (17, lin-123)", report.Serial, report.Lineage)
	}
}

// TestCollectStampsLineageSerialHint proves a single-state complete result
// carries the in-tree `lineage=<uuid> serial=<n>` freshness shape the host
// requires on every committed terraform-state claim.
func TestCollectStampsLineageSerialHint(t *testing.T) {
	t.Parallel()

	report := Report{GeneratedAt: time.Now().UTC(), Source: "s3://b/k", Serial: 17, Lineage: "lin-123"}
	report.Records = append(report.Records, Record{ID: "aws_s3_bucket.one", Name: "aws_s3_bucket", Detail: "managed"})
	result, err := Collect(liveTestClaim(), report, CollectOptions{ObservedAt: time.Now().UTC(), SourceURI: "s3://b/k"})
	if err != nil {
		t.Fatalf("Collect error = %v", err)
	}
	if result.State != sdk.ResultComplete {
		t.Fatalf("State = %q, want complete", result.State)
	}
	if want := "lineage=lin-123 serial=17"; result.Generation.FreshnessHint != want {
		t.Fatalf("FreshnessHint = %q, want %q", result.Generation.FreshnessHint, want)
	}
}
