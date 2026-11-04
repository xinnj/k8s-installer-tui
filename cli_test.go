package main

// Tests for command-line option parsing. They run without root, podman or a
// terminal.

import (
	"errors"
	"flag"
	"testing"
)

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    cliOptions
		wantErr bool
	}{
		{name: "no args", args: nil, want: cliOptions{}},
		{name: "long version", args: []string{"--version"}, want: cliOptions{showVersion: true}},
		{name: "short version", args: []string{"-v"}, want: cliOptions{showVersion: true}},
		{name: "short config", args: []string{"-c", "cluster.yaml"}, want: cliOptions{configPath: "cluster.yaml"}},
		{name: "long config", args: []string{"--config", "cluster.yaml"}, want: cliOptions{configPath: "cluster.yaml"}},
		{name: "config equals", args: []string{"--config=cluster.yaml"}, want: cliOptions{configPath: "cluster.yaml"}},
		{name: "dry run", args: []string{"-c", "cluster.yaml", "--dry-run"}, want: cliOptions{configPath: "cluster.yaml", dryRun: true}},
		{name: "dry run without config", args: []string{"--dry-run"}, wantErr: true},
		{name: "config without value", args: []string{"--config"}, wantErr: true},
		{name: "empty config", args: []string{"--config="}, wantErr: true},
		{name: "empty short config", args: []string{"-c", ""}, wantErr: true},
		{name: "unknown flag", args: []string{"--nope"}, wantErr: true},
		{name: "positional argument", args: []string{"extra"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOptions(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseOptions(%v) = nil error, want error", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOptions(%v) error: %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("parseOptions(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseOptionsHelp(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			_, err := parseOptions([]string{arg})
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("parseOptions(%q) error = %v, want flag.ErrHelp", arg, err)
			}
		})
	}
}
