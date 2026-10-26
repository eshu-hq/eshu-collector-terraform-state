// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"encoding/json"
	"strings"
	"testing"
)

// The in-tree terraformstate package exercises these streaming helpers
// through parser tests that need the core-owned redact engine and schema
// bundle. These contract tests pin the ported behavior directly.
func TestReadOpeningDelimAcceptsMatchingDelim(t *testing.T) {
	t.Parallel()

	if err := readOpeningDelim(json.NewDecoder(strings.NewReader(`{"a":1}`)), '{', "root"); err != nil {
		t.Fatalf("readOpeningDelim() error = %v, want nil", err)
	}
	if err := readOpeningDelim(json.NewDecoder(strings.NewReader(`[1,2]`)), '[', "root"); err != nil {
		t.Fatalf("readOpeningDelim() error = %v, want nil", err)
	}
}

func TestReadOpeningDelimRejectsMismatch(t *testing.T) {
	t.Parallel()

	if err := readOpeningDelim(json.NewDecoder(strings.NewReader(`[1,2]`)), '{', "root"); err == nil {
		t.Fatal("readOpeningDelim() error = nil for array-as-object, want non-nil")
	}
}

func TestReadStringDecodesValue(t *testing.T) {
	t.Parallel()

	got, err := readString(json.NewDecoder(strings.NewReader(`"lineage-123"`)), "lineage")
	if err != nil {
		t.Fatalf("readString() error = %v, want nil", err)
	}
	if got != "lineage-123" {
		t.Fatalf("readString() = %q, want %q", got, "lineage-123")
	}
	if _, err := readString(json.NewDecoder(strings.NewReader(`42`)), "lineage"); err == nil {
		t.Fatal("readString() error = nil for number, want non-nil")
	}
}

func TestSkipValueSkipsNestedComposite(t *testing.T) {
	t.Parallel()

	decoder := json.NewDecoder(strings.NewReader(`{"skip":{"a":[1,{"b":2}]},"keep":true}`))
	if err := readOpeningDelim(decoder, '{', "root"); err != nil {
		t.Fatalf("readOpeningDelim() error = %v, want nil", err)
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("Token() error = %v, want nil", err)
		}
		if token == "skip" {
			if err := skipValue(decoder); err != nil {
				t.Fatalf("skipValue() error = %v, want nil", err)
			}
			continue
		}
		if err := skipValue(decoder); err != nil {
			t.Fatalf("skipValue() error = %v, want nil", err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("close object error = %v, want nil", err)
	}
}

func TestReadScalarOrSkipReturnsScalarsAndSkipsComposites(t *testing.T) {
	t.Parallel()

	decoder := json.NewDecoder(strings.NewReader(`[1, {"a": 2}, "x"]`))
	if err := readOpeningDelim(decoder, '[', "root"); err != nil {
		t.Fatalf("readOpeningDelim() error = %v, want nil", err)
	}
	value, scalar, err := readScalarOrSkip(decoder)
	if err != nil || !scalar || value == nil {
		t.Fatalf("readScalarOrSkip() = (%v, %v, %v), want scalar 1", value, scalar, err)
	}
	value, scalar, err = readScalarOrSkip(decoder)
	if err != nil || scalar {
		t.Fatalf("readScalarOrSkip() = (%v, %v, %v), want composite skipped", value, scalar, err)
	}
	value, scalar, err = readScalarOrSkip(decoder)
	if err != nil || !scalar || value != "x" {
		t.Fatalf("readScalarOrSkip() = (%v, %v, %v), want scalar x", value, scalar, err)
	}
}

func TestExpectEOFAcceptsCleanEndAndRejectsTrailingContent(t *testing.T) {
	t.Parallel()

	if err := expectEOF(json.NewDecoder(strings.NewReader(""))); err != nil {
		t.Fatalf("expectEOF() error = %v for empty input, want nil", err)
	}
	if err := expectEOF(json.NewDecoder(strings.NewReader(`{} {} `))); err == nil {
		t.Fatal("expectEOF() error = nil for trailing content, want non-nil")
	}
}
