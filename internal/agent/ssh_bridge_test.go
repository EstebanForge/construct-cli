package agent

import (
	"testing"
)

func TestSSHBridgePortForSeed(t *testing.T) {
	// Deterministic: same seed -> same port.
	p1 := sshBridgePortForSeed("construct-cli-deadbeef")
	p2 := sshBridgePortForSeed("construct-cli-deadbeef")
	if p1 != p2 {
		t.Fatalf("deterministic port changed for same seed: %d vs %d", p1, p2)
	}

	// Stable across the box lifecycle: the entrypoint bakes this port at
	// container creation, so it must not vary between invocations.
	for i := 0; i < 10; i++ {
		if got := sshBridgePortForSeed("construct-cli-deadbeef"); got != p1 {
			t.Fatalf("port drifted across calls: %d vs %d", got, p1)
		}
	}

	// Within the advertised band.
	if p1 < sshBridgePortBase || p1 >= sshBridgePortBase+sshBridgePortSpan {
		t.Fatalf("port %d outside band [%d,%d)", p1, sshBridgePortBase, sshBridgePortBase+sshBridgePortSpan)
	}

	// Different boxes should not trivially collide on the same port.
	seen := make(map[int]string)
	collisions := 0
	for _, seed := range []string{
		"construct-cli-aaaaaaaa",
		"construct-cli-bbbbbbbb",
		"construct-cli-cccccccc",
		"construct-cli-dddddddd",
		"construct-cli-eeeeeeee",
		"construct-cli-ffffffff",
		"construct-cli-11111111",
		"construct-cli-22222222",
	} {
		port := sshBridgePortForSeed(seed)
		if other, dup := seen[port]; dup {
			t.Logf("collision: %q and %q -> %d (acceptable within a %d-wide band)", seed, other, port, sshBridgePortSpan)
			collisions++
		}
		seen[port] = seed
	}
	// With an 8-sample draw from a 10k-wide band, expecting near-zero collisions.
	if collisions > 1 {
		t.Fatalf("too many port collisions (%d) for 8 distinct seeds", collisions)
	}
}
