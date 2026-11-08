package main

import "testing"

func TestShouldRunMigration(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "empty args", args: []string{}, want: false},
		{name: "sys doctor", args: []string{"sys", "doctor"}, want: true},
		{name: "sys image pull", args: []string{"sys", "image", "pull"}, want: true},
		{name: "sys image rebuild", args: []string{"sys", "image", "rebuild"}, want: false},
		{name: "sys agents", args: []string{"sys", "agents"}, want: true},
		{name: "sys self-update", args: []string{"sys", "self-update"}, want: false},
		{name: "sys typo", args: []string{"sys", "rebuilt"}, want: false},
		{name: "network list", args: []string{"network", "list"}, want: true},
		{name: "network explain", args: []string{"network", "explain"}, want: true},
		{name: "net explain", args: []string{"net", "explain"}, want: true},
		{name: "net log", args: []string{"net", "log"}, want: true},
		{name: "network typo", args: []string{"network", "lst"}, want: false},
		{name: "claude", args: []string{"claude"}, want: true},
		{name: "supported agent", args: []string{"codex"}, want: true},
		{name: "unknown command", args: []string{"not-a-command"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRunMigration(tt.args)
			if got != tt.want {
				t.Fatalf("shouldRunMigration(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
