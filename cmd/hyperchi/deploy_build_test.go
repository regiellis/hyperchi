package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployBuildCommandValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"required name", []string{"deploy", "build"}, "required flag"},
		{"unknown target", []string{"deploy", "build", "--name", "demo", "--target", "unknown"}, "target must"},
		{"extra argument", []string{"deploy", "build", "--name", "demo", "extra"}, "unknown command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRootCmd()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(append(tt.args, "--output", filepath.Join(t.TempDir(), "out")))
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v; want %q", err, tt.want)
			}
		})
	}
}
