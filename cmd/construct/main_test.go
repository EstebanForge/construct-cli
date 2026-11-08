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
		{name: "sys image init", args: []string{"sys", "image", "init"}, want: true},
		{name: "sys image reset", args: []string{"sys", "image", "reset"}, want: true},
		{name: "sys image rebuild", args: []string{"sys", "image", "rebuild"}, want: false},
		{name: "sys image help", args: []string{"sys", "image", "help"}, want: false},
		{name: "sys image bare", args: []string{"sys", "image"}, want: false},
		{name: "sys shims install", args: []string{"sys", "shims", "install"}, want: true},
		{name: "sys shims uninstall", args: []string{"sys", "shims", "uninstall"}, want: true},
		{name: "sys shims list", args: []string{"sys", "shims", "list"}, want: false},
		{name: "sys shims bare", args: []string{"sys", "shims"}, want: false},
		{name: "sys agents", args: []string{"sys", "agents"}, want: true},
		{name: "sys self-update", args: []string{"sys", "self-update"}, want: false},
		{name: "sys typo", args: []string{"sys", "rebuilt"}, want: false},
		{name: "sys network list", args: []string{"sys", "network", "list"}, want: true},
		{name: "sys network explain", args: []string{"sys", "network", "explain"}, want: true},
		{name: "sys net explain", args: []string{"sys", "net", "explain"}, want: true},
		{name: "sys net log", args: []string{"sys", "net", "log"}, want: true},
		{name: "sys network typo", args: []string{"sys", "network", "lst"}, want: false},
		{name: "sys auth", args: []string{"sys", "auth"}, want: true},
		{name: "sys set-password removed", args: []string{"sys", "set-password"}, want: false},
		{name: "top-level network removed", args: []string{"network", "list"}, want: false},
		{name: "top-level net removed", args: []string{"net", "explain"}, want: false},
		{name: "top-level doctor", args: []string{"doctor"}, want: true},
		{name: "top-level agents", args: []string{"agents"}, want: true},
		{name: "top-level update", args: []string{"update"}, want: true},
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
