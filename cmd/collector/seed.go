// SPDX-License-Identifier: MIT
// Copyright (c) 2025-2026 eshu-hq

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	collector "github.com/eshu-hq/eshu-collector-terraform-state"
)

var validImageRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-/:@]*:[A-Za-z0-9][A-Za-z0-9._\-]*(@sha256:[0-9a-f]{64})?$`)

// runSeed implements the `seed` subcommand: write this binary plus the
// stamped manifest into dest for the init-container install pattern.
func runSeed(args []string, stdout io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	dest := fs.String("dest", "", "directory to install the satellite binary and manifest into")
	imageRef := fs.String("image-ref", "", "full image reference (name:tag@sha256:digest) to stamp into the manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dest == "" {
		return errors.New("seed: --dest is required")
	}
	if !validImageRef.MatchString(*imageRef) {
		return fmt.Errorf("seed: invalid --image-ref %q: want name:tag[@sha256:digest]", *imageRef)
	}
	if err := os.MkdirAll(*dest, 0o755); err != nil { // #nosec G301 -- seed dir must be executable to run the installed binary
		return fmt.Errorf("seed: create dest: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("seed: locate own executable: %w", err)
	}
	digest, err := copyExecutable(exe, filepath.Join(*dest, "collector"))
	if err != nil {
		return err
	}
	manifest := string(collector.ManifestYAML)
	if manifest == "" {
		return errors.New("seed: embedded manifest is empty")
	}
	// The manifest embeds `image: <placeholder>` when the build pins no
	// digest; replace it with the deployed reference. A config env override
	// wins when set so operators can retarget in place.
	target := *imageRef
	if getenv != nil {
		if override := getenv("ESHU_COLLECTOR_IMAGE"); override != "" {
			target = override
		}
	}
	stamped := stampImageRef(manifest, target)
	if err := writeAtomic(filepath.Join(*dest, "manifest.yaml"), 0o644, func(w io.Writer) error {
		_, err := io.WriteString(w, stamped)
		return err
	}); err != nil {
		return fmt.Errorf("seed: write manifest: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "seeded %s image=%s sha256=%s\n", filepath.Join(*dest, "collector"), target, digest)
	return nil
}

func copyExecutable(source string, target string) (string, error) {
	in, err := os.Open(source) // #nosec G304 -- the path is this process's own executable
	if err != nil {
		return "", errors.New("seed: open own executable")
	}
	defer func() { _ = in.Close() }()
	hash := sha256.New()
	if err := writeAtomic(target, 0o755, func(w io.Writer) error {
		_, err := io.Copy(io.MultiWriter(w, hash), in)
		return err
	}); err != nil {
		return "", errors.New("seed: write binary")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// writeAtomic writes through a temporary file in the target directory and
// renames it into place, so a concurrent reader never sees a partial file.
func writeAtomic(target string, mode os.FileMode, write func(io.Writer) error) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".seed-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err = write(tmp); err != nil {
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

// stampImageRef replaces an `image: <placeholder>` line with the deployed
// reference. When the manifest has no placeholder line, the document is
// returned unchanged.
func stampImageRef(manifest string, imageRef string) string {
	lines := strings.Split(manifest, "\n")
	stamped := false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !stamped && strings.HasPrefix(trimmed, "image:") {
			lines[i] = line[:len(line)-len(trimmed)] + "image: " + imageRef
			stamped = true
		}
	}
	return strings.Join(lines, "\n")
}
