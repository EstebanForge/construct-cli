package runtime

import (
	"net"
	"testing"
)

func TestListenInBridgeBandPortInBand(t *testing.T) {
	listener, err := ListenInBridgeBand("test-seed-in-band", "127.0.0.1")
	if err != nil {
		t.Fatalf("ListenInBridgeBand: %v", err)
	}
	defer func() { _ = listener.Close() }()

	port := listener.Addr().(*net.TCPAddr).Port
	if port < MsbBridgePortBase || port >= MsbBridgePortBase+MsbBridgePortSpan {
		t.Fatalf("port %d outside bridge band [%d,%d)", port, MsbBridgePortBase, MsbBridgePortBase+MsbBridgePortSpan)
	}
}

func TestListenInBridgeBandSkipsTakenCandidate(t *testing.T) {
	first, err := ListenInBridgeBand("test-seed-collision", "127.0.0.1")
	if err != nil {
		t.Fatalf("first listener: %v", err)
	}
	defer func() { _ = first.Close() }()

	// Same seed derives the same first candidate; the probe must walk to
	// the next free port instead of failing or reusing the held one.
	second, err := ListenInBridgeBand("test-seed-collision", "127.0.0.1")
	if err != nil {
		t.Fatalf("second listener: %v", err)
	}
	defer func() { _ = second.Close() }()

	firstPort := first.Addr().(*net.TCPAddr).Port
	secondPort := second.Addr().(*net.TCPAddr).Port
	if firstPort == secondPort {
		t.Fatalf("probe reused taken port %d", firstPort)
	}
	if secondPort < MsbBridgePortBase || secondPort >= MsbBridgePortBase+MsbBridgePortSpan {
		t.Fatalf("fallback port %d outside bridge band", secondPort)
	}
}

func TestBridgeBandCandidateDeterministicAndInBand(t *testing.T) {
	// Pure derivation: the same seed always computes the same preferred
	// port, which is what lets a per-run bridge land on a port the
	// daemon's create-time rules already allow.
	for _, seed := range []string{"", "box", "construct-cli-daemon/clipboard"} {
		a := bandCandidate(seed, 0, MsbBridgePortBase, MsbBridgePortSpan)
		b := bandCandidate(seed, 0, MsbBridgePortBase, MsbBridgePortSpan)
		if a != b {
			t.Fatalf("candidate not deterministic for seed %q: %d vs %d", seed, a, b)
		}
		if a < MsbBridgePortBase || a >= MsbBridgePortBase+MsbBridgePortSpan {
			t.Fatalf("candidate %d outside bridge band for seed %q", a, seed)
		}
	}

	if bandCandidate("a", 0, MsbBridgePortBase, MsbBridgePortSpan) == bandCandidate("b", 0, MsbBridgePortBase, MsbBridgePortSpan) && bandCandidate("a", 1, MsbBridgePortBase, MsbBridgePortSpan) == bandCandidate("a", 0, MsbBridgePortBase, MsbBridgePortSpan) {
		t.Fatal("candidates do not vary across seeds or probe indices")
	}
}

func TestListenInHerdrBandStaysInHerdrBand(t *testing.T) {
	listener, err := ListenInBand("test-herdr-seed", "127.0.0.1", MsbHerdrBridgePortBase, MsbHerdrBridgePortSpan)
	if err != nil {
		t.Fatalf("ListenInBand: %v", err)
	}
	defer func() { _ = listener.Close() }()

	port := listener.Addr().(*net.TCPAddr).Port
	if port < MsbHerdrBridgePortBase || port >= MsbHerdrBridgePortBase+MsbHerdrBridgePortSpan {
		t.Fatalf("port %d outside herdr band [%d,%d)", port, MsbHerdrBridgePortBase, MsbHerdrBridgePortBase+MsbHerdrBridgePortSpan)
	}
}
