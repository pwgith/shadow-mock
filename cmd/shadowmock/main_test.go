package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) (string, bool) { return "", false }

func TestRun(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run([]string{"-version"}, noEnv, &out); err != nil || !strings.Contains(out.String(), version) {
		t.Errorf("run(-version) = %v, %q", err, out.String())
	}
	if err := run([]string{"-nope"}, noEnv, &out); err == nil {
		t.Error("an unknown flag should fail")
	}
	if err := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}, noEnv, &out); err == nil {
		t.Error("a missing config file should fail")
	}
}
