// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package collector

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// workflowStep holds the step fields the CI shape tests read. Other tests
// ignore the rest.
type workflowStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

// workflowFile holds the workflow fields the CI shape tests read.
type workflowFile struct {
	Jobs map[string]struct {
		If    string         `yaml:"if"`
		Steps []workflowStep `yaml:"steps"`
	} `yaml:"jobs"`
}

// TestImageLaneRunsOnGoSources keeps the shipped artifact validated on
// PRs that change what it embeds: the Dockerfile COPYs go.mod/go.sum,
// COPYs the whole context (which keeps .go sources), and go-builds the
// binary, so a Go-source or go.mod PR must select the image lane. A
// go.mod toolchain bump past the pinned base would otherwise pass every
// Go lane yet break docker build, surfacing only as post-merge red main.
func TestImageLaneRunsOnGoSources(t *testing.T) {
	t.Parallel()

	workflow := readCIWorkflow(t)
	job, ok := workflow.Jobs["image"]
	if !ok {
		t.Fatal("ci.yml has no image job")
	}
	if !strings.Contains(job.If, "needs.changes.outputs.go == 'true'") {
		t.Errorf("job image if = %q, want the go selecting clause: Go sources ship in the image", job.If)
	}
	if strings.Contains(job.If, "needs.changes.outputs.go !=") {
		t.Errorf("job image if = %q, want == 'true', not an inverted comparison", job.If)
	}
	if strings.Contains(job.If, "needs.changes.outputs.go == 'false'") {
		t.Errorf("job image if = %q, want == 'true', not == 'false'", job.If)
	}

	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	goIdx := strings.Index(text, "\n            go:\n")
	if goIdx < 0 {
		t.Fatal("ci.yml changes job has no go: filter")
	}
	rest := text[goIdx:]
	end := strings.Index(rest, "\n            fixtures:")
	if end < 0 {
		t.Fatal("ci.yml changes job has no fixtures: filter after go:")
	}
	if !strings.Contains(rest[:end], "'**/*.go'") {
		t.Errorf("ci.yml go: filter lacks '**/*.go': Go sources would not select the Go lanes")
	}
}

func readCIWorkflow(t *testing.T) workflowFile {
	t.Helper()

	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.Jobs) == 0 {
		t.Fatal("ci.yml defines no jobs")
	}
	return workflow
}
