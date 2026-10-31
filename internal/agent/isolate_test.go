package agent

import (
	"reflect"
	"testing"
)

func TestWrapIsolatedCommand(t *testing.T) {
	// If workdir is empty or outside /workspaces, it should return original command
	cmd := []string{"bash", "-lc", "echo hi"}
	if got := wrapIsolatedCommand("", cmd); !reflect.DeepEqual(got, cmd) {
		t.Fatalf("expected untouched cmd for empty workdir, got: %v", got)
	}

	if got := wrapIsolatedCommand("/home/construct", cmd); !reflect.DeepEqual(got, cmd) {
		t.Fatalf("expected untouched cmd for non-workspaces workdir, got: %v", got)
	}

	// Inside /workspaces, it wraps in sudo -E -n unshare
	target := "/workspaces/Dev-8d30c9fa/EstebanForge/construct-cli"
	wrapped := wrapIsolatedCommand(target, cmd)
	if len(wrapped) < 5 {
		t.Fatalf("wrapped cmd too short: %v", wrapped)
	}
	if wrapped[0] != "sudo" || wrapped[1] != "-E" || wrapped[2] != "-n" || wrapped[3] != "unshare" {
		t.Fatalf("unexpected wrapper prefix: %v", wrapped[:4])
	}
	// Verify last element contains the inner command args
	last := wrapped[len(wrapped)-1]
	if last != "echo hi" {
		t.Fatalf("expected last arg to be 'echo hi', got: %v", last)
	}
}
