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

const (
	seededBinaryName   = "collector"
	seededManifestName = "manifest.yaml"
	// imageRefEnv supplies the digest-pinned image reference when --image-ref
	// is not given; the flag wins.
	imageRefEnv = "COLLECTOR_IMAGE_REF"
)

var (
	// imageRefPattern is the only reference shape seed stamps into a
	// manifest: a lowercase repository (dot, dash, or underscore separated
	// path segments, with an optional registry port), then a full sha256
	// digest. It admits no quote, "$", "#", bracket, whitespace, or other
	// YAML-significant character, so a stamped line is always a plain scalar.
	// Go's "$" matches only at the end of the text, never before a final
	// newline.
	imageRefPattern = regexp.MustCompile(
		`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)*@sha256:[0-9a-f]{64}$`)
	manifestImageLinePattern = regexp.MustCompile(`(?m)^([ \t]*image:[ \t]*)\S+[ \t]*$`)
)

// runSeed implements `collector seed --dest DIR [--image-ref REF@sha256:DIGEST]`.
// It writes DIR/collector (this binary, mode 0755) and DIR/manifest.yaml (the
// embedded manifest, mode 0644). Re-running it is safe: files are replaced
// atomically with identical content. --image-ref, or the COLLECTOR_IMAGE_REF
// environment variable when the flag is absent, stamps the digest-pinned image
// reference the operator deployed into the manifest's artifact entry, because
// an image cannot embed its own digest. One trailing newline is trimmed from
// the value (an environment variable often carries one); anything else that
// does not match imageRefPattern exits non-zero before any file is written,
// and the error never echoes the value. Without a reference, the repository
// placeholder is written unchanged.
func runSeed(args []string, stdout io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dest := flags.String("dest", "", "directory to write the collector binary and manifest into")
	imageRef := flags.String("image-ref", "", "optional digest-pinned image reference (name@sha256:<64 lowercase hex>) to record in the manifest artifact; defaults to $"+imageRefEnv)
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("seed: unexpected argument %q", flags.Arg(0))
	}
	executable, err := os.Executable()
	if err != nil {
		return errors.New("seed: resolve own executable")
	}
	manifest, err := manifestFor(collector.ManifestYAML, selectImageRef(*imageRef, getenv))
	if err != nil {
		return err
	}
	digest, err := seed(*dest, executable, manifest)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "seeded %s sha256:%s\n", filepath.Join(*dest, seededBinaryName), digest)
	return err
}

// selectImageRef returns the reference to stamp: the flag value when it is not
// empty, otherwise the environment variable. Each has one trailing newline
// trimmed first, so a lone newline counts as unset. Nothing else is trimmed.
func selectImageRef(flagValue string, getenv func(string) string) string {
	if ref := strings.TrimSuffix(flagValue, "\n"); ref != "" {
		return ref
	}
	if getenv == nil {
		return ""
	}
	return strings.TrimSuffix(getenv(imageRefEnv), "\n")
}

// manifestFor returns the manifest bytes to seed, stamping the image reference
// into the artifact entry when one is given. An empty image leaves the
// manifest unchanged; any other value must match imageRefPattern exactly.
func manifestFor(manifest []byte, image string) ([]byte, error) {
	if image == "" {
		return manifest, nil
	}
	if !imageRefPattern.MatchString(image) {
		return nil, errors.New("seed: image reference must be name@sha256:<64 lowercase hex digits>")
	}
	return stampImage(manifest, image)
}

// stampImage returns manifest with its one artifact image set to image,
// changing nothing else. It replaces literally: a template replacement would
// expand "$1" and "${name}" inside the reference and silently alter it. It
// fails when the manifest does not declare exactly one artifact image, so a
// stamp is never dropped or applied twice.
func stampImage(manifest []byte, image string) ([]byte, error) {
	if len(manifestImageLinePattern.FindAllIndex(manifest, -1)) != 1 {
		return nil, errors.New("seed: manifest must declare exactly one artifact image to stamp")
	}
	return manifestImageLinePattern.ReplaceAllFunc(manifest, func(line []byte) []byte {
		prefix := manifestImageLinePattern.FindSubmatch(line)[1]
		return append(append([]byte(nil), prefix...), image...)
	}), nil
}

// seed copies the executable and writes the manifest into dest, returning the
// hex SHA-256 of the seeded binary.
func seed(dest string, executable string, manifest []byte) (string, error) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", errors.New("seed: --dest is required")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil { // #nosec G301 -- seed dir must be executable to run the installed binary
		return "", errors.New("seed: create destination directory")
	}
	digest, err := copyExecutable(executable, filepath.Join(dest, seededBinaryName))
	if err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dest, seededManifestName), 0o644, func(w io.Writer) error {
		_, err := w.Write(manifest)
		return err
	}); err != nil {
		return "", errors.New("seed: write manifest")
	}
	return digest, nil
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
