// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"errors"
	"reflect"
	"testing"
)

// The in-tree terraformstate package has no unit tests for these pure
// helpers. These contract tests pin the ported behavior so a future
// full-port diff has a frozen baseline.
func TestMergeContractPayloadKeepsExistingKeys(t *testing.T) {
	t.Parallel()

	payload := map[string]any{"a": "original", "b": 1}
	if err := mergeContractPayload(payload, func() (map[string]any, error) {
		return map[string]any{"a": "override", "c": true}, nil
	}); err != nil {
		t.Fatalf("mergeContractPayload() error = %v, want nil", err)
	}
	want := map[string]any{"a": "original", "b": 1, "c": true}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("mergeContractPayload() payload = %#v, want %#v", payload, want)
	}
}

func TestMergeContractPayloadPropagatesEncodeError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("encode boom")
	payload := map[string]any{}
	if err := mergeContractPayload(payload, func() (map[string]any, error) { return nil, sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("mergeContractPayload() error = %v, want %v", err, sentinel)
	}
	mergeContractPayloadNoError(payload, func() (map[string]any, error) { return nil, sentinel })
}

func TestPointerAndPayloadHelpers(t *testing.T) {
	t.Parallel()

	if got := stringPtr("x"); got == nil || *got != "x" {
		t.Fatalf("stringPtr() = %v, want pointer to x", got)
	}
	if optionalStringPtr("") != nil {
		t.Fatal("optionalStringPtr(\"\") != nil, want nil")
	}
	if got := optionalStringPtr("x"); got == nil || *got != "x" {
		t.Fatalf("optionalStringPtr(x) = %v, want pointer to x", got)
	}
	if got := boolPtr(true); got == nil || !*got {
		t.Fatalf("boolPtr() = %v, want pointer to true", got)
	}
	if got := int64Ptr(17); got == nil || *got != 17 {
		t.Fatalf("int64Ptr() = %v, want pointer to 17", got)
	}
	payload := map[string]any{"s": "v", "b": true, "n": 1}
	if got := payloadString(payload, "s"); got != "v" {
		t.Fatalf("payloadString() = %q, want v", got)
	}
	if got := payloadString(payload, "n"); got != "" {
		t.Fatalf("payloadString() for non-string = %q, want empty", got)
	}
	if got := optionalStringPtrFromPayload(payload, "s"); got == nil || *got != "v" {
		t.Fatalf("optionalStringPtrFromPayload() = %v, want pointer to v", got)
	}
	if optionalStringPtrFromPayload(payload, "missing") != nil {
		t.Fatal("optionalStringPtrFromPayload() for missing key != nil, want nil")
	}
	if got := optionalBoolPtrFromPayload(payload, "b"); got == nil || !*got {
		t.Fatalf("optionalBoolPtrFromPayload() = %v, want pointer to true", got)
	}
	if optionalBoolPtrFromPayload(payload, "s") != nil {
		t.Fatal("optionalBoolPtrFromPayload() for string value != nil, want nil")
	}
}

func TestTypedCorrelationAnchorsFiltersNonMaps(t *testing.T) {
	t.Parallel()

	if typedCorrelationAnchors(nil) != nil {
		t.Fatal("typedCorrelationAnchors(nil) != nil, want nil")
	}
	if typedCorrelationAnchors([]any{"nope", 42}) != nil {
		t.Fatal("typedCorrelationAnchors() without maps != nil, want nil")
	}
	got := typedCorrelationAnchors([]any{map[string]any{"k": "v"}, "nope"})
	want := []map[string]any{{"k": "v"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typedCorrelationAnchors() = %#v, want %#v", got, want)
	}
}
