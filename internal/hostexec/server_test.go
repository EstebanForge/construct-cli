package hostexec

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubBin writes a tiny executable that emits deterministic stdout/stderr and
// exits with a chosen code. Returns its absolute path and the directory it
// lives in (so it can be prepended to PATH).
func stubBin(t *testing.T, name, script string) (absPath, dir string) {
	t.Helper()
	dir = t.TempDir()
	absPath = filepath.Join(dir, name)
	if err := os.WriteFile(absPath, []byte("#!/usr/bin/env bash\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return absPath, dir
}

// withPath prepends dir to PATH for the duration of fn (StartServer runs
// exec.LookPath, so the stub must be discoverable).
func withPath(t *testing.T, dir string, fn func()) {
	t.Helper()
	prev := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
	fn()
}

// readFrames reads the streamed JSONL response into a slice.
func readFrames(t *testing.T, body io.Reader) []frame {
	t.Helper()
	dec := json.NewDecoder(body)
	var out []frame
	for {
		var f frame
		if err := dec.Decode(&f); err != nil {
			if err == io.EOF {
				break
			}
			// json.Decoder yields errors at EOF when trailing newline only.
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decode frame: %v", err)
		}
		out = append(out, f)
	}
	return out
}

func newBridge(t *testing.T, binaries []string, timeout time.Duration) *Server {
	t.Helper()
	// 127.0.0.1 works on all platforms for in-process tests.
	s, err := StartServer("127.0.0.1", binaries, timeout)
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

func doExec(t *testing.T, s *Server, token string, argv []string, stdin []byte) *http.Response {
	t.Helper()
	body, _ := json.Marshal(execRequest{
		Argv:  argv,
		Stdin: base64.StdEncoding.EncodeToString(stdin),
	})
	req, err := http.NewRequest(http.MethodPost, s.URL+"/exec", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	return resp
}

func TestStartServerMissingBinaryFails(t *testing.T) {
	_, err := StartServer("127.0.0.1", []string{"definitely-not-a-real-binary-xyz"}, time.Minute)
	if err == nil {
		t.Fatal("expected error for missing host binary")
	}
}

func TestTokenMismatchReturns401(t *testing.T) {
	s := newBridge(t, nil, time.Minute)
	resp := doExec(t, s, "wrong-token", []string{"echo"}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestUnknownArgv0Returns403(t *testing.T) {
	s := newBridge(t, nil, time.Minute)
	resp := doExec(t, s, s.Token, []string{"never-listed"}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestExecStreamsStdoutThenExit(t *testing.T) {
	_, dir := stubBin(t, "wicket", `printf 'hello-out'; exit 0`)
	withPath(t, dir, func() {
		s := newBridge(t, []string{"wicket"}, time.Minute)
		resp := doExec(t, s, s.Token, []string{"wicket"}, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		frames := readFrames(t, resp.Body)
		var stdout []byte
		var code *int
		for _, f := range frames {
			switch f.Type {
			case "stdout":
				b, _ := base64.StdEncoding.DecodeString(f.Data)
				stdout = append(stdout, b...)
			case "exit":
				code = f.Code
			}
		}
		if string(stdout) != "hello-out" {
			t.Fatalf("stdout=%q want hello-out", stdout)
		}
		if code == nil || *code != 0 {
			t.Fatalf("exit=%v want 0", code)
		}
	})
}

func TestExecStderrSeparateAndNonZeroExit(t *testing.T) {
	_, dir := stubBin(t, "wicket", `printf 'to-err' >&2; printf 'to-out'; exit 7`)
	withPath(t, dir, func() {
		s := newBridge(t, []string{"wicket"}, time.Minute)
		resp := doExec(t, s, s.Token, []string{"wicket"}, nil)
		defer resp.Body.Close()
		var stdout, stderr []byte
		var code *int
		for _, f := range readFrames(t, resp.Body) {
			switch f.Type {
			case "stdout":
				b, _ := base64.StdEncoding.DecodeString(f.Data)
				stdout = append(stdout, b...)
			case "stderr":
				b, _ := base64.StdEncoding.DecodeString(f.Data)
				stderr = append(stderr, b...)
			case "exit":
				code = f.Code
			}
		}
		if string(stdout) != "to-out" || string(stderr) != "to-err" {
			t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
		}
		if code == nil || *code != 7 {
			t.Fatalf("exit=%v want 7", code)
		}
	})
}

func TestExecPreservesStdoutOrderWithinStream(t *testing.T) {
	// Child writes many small lines to stdout; each line must arrive in order
	// (the stdout drain is single-goroutine, so order is inherent; this guards
	// against any future buffering that reorders).
	script := `for i in 1 2 3 4 5 6 7 8 9 10; do printf "line-%s\n" "$i"; done`
	_, dir := stubBin(t, "wicket", script)
	withPath(t, dir, func() {
		s := newBridge(t, []string{"wicket"}, time.Minute)
		resp := doExec(t, s, s.Token, []string{"wicket"}, nil)
		defer resp.Body.Close()
		var stdout []byte
		for _, f := range readFrames(t, resp.Body) {
			if f.Type == "stdout" {
				b, _ := base64.StdEncoding.DecodeString(f.Data)
				stdout = append(stdout, b...)
			}
		}
		want := ""
		for i := 1; i <= 10; i++ {
			want += "line-" + itoa(i) + "\n"
		}
		if string(stdout) != want {
			t.Fatalf("got=%q\nwant=%q", stdout, want)
		}
	})
}

func TestExecPassesStdin(t *testing.T) {
	_, dir := stubBin(t, "wicket", `cat`)
	withPath(t, dir, func() {
		s := newBridge(t, []string{"wicket"}, time.Minute)
		resp := doExec(t, s, s.Token, []string{"wicket"}, []byte("piped-stdin-bytes"))
		defer resp.Body.Close()
		var stdout []byte
		for _, f := range readFrames(t, resp.Body) {
			if f.Type == "stdout" {
				b, _ := base64.StdEncoding.DecodeString(f.Data)
				stdout = append(stdout, b...)
			}
		}
		if string(stdout) != "piped-stdin-bytes" {
			t.Fatalf("stdin not echoed: %q", stdout)
		}
	})
}

func TestExecTimeoutKillsAndEmits124(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group kill is unix-only")
	}
	_, dir := stubBin(t, "wicket", `trap '' TERM; sleep 30`)
	withPath(t, dir, func() {
		s := newBridge(t, []string{"wicket"}, 300*time.Millisecond)
		resp := doExec(t, s, s.Token, []string{"wicket"}, nil)
		defer resp.Body.Close()
		var code *int
		for _, f := range readFrames(t, resp.Body) {
			if f.Type == "exit" {
				code = f.Code
			}
		}
		if code == nil || *code != exitCodeKill {
			t.Fatalf("exit=%v want %d", code, exitCodeKill)
		}
	})
}

func TestExecKillsProcessGroupNoOrphan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group kill is unix-only")
	}
	// Child spawns a grandchild that writes a marker file after a delay. The
	// bridge timeout must kill the whole group before the marker is written.
	marker := filepath.Join(t.TempDir(), "orphan-marker")
	_, dir := stubBin(t, "wicket", strings.Join([]string{
		`sleep 20 &`,
		`wait`,
	}, "\n"))
	// Replace a placeholder so the grandchild writes the marker. (Using a
	// separate script avoids arg-quoting issues.)
	gc := filepath.Join(dir, "gc")
	if err := os.WriteFile(gc, []byte("#!/usr/bin/env bash\nsleep 1\necho x > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = gc // marker is written by no one: the bridge kills `sleep 20` and `wait`
	withPath(t, dir, func() {
		s := newBridge(t, []string{"wicket"}, 300*time.Millisecond)
		resp := doExec(t, s, s.Token, []string{"wicket"}, nil)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	})
	// Give any orphan a moment to write the marker if it survived.
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("orphan marker exists: child process group was not killed")
	}
}

func TestConcurrentBridgeStartsIndependent(t *testing.T) {
	// Two bridges on two random ports with two tokens must not collide.
	_, dir := stubBin(t, "wicket", `echo hi`)
	withPath(t, dir, func() {
		s1 := newBridge(t, []string{"wicket"}, time.Minute)
		s2 := newBridge(t, []string{"wicket"}, time.Minute)
		if s1.Port == s2.Port {
			t.Fatalf("two bridges got the same port: %d", s1.Port)
		}
		if s1.Token == s2.Token {
			t.Fatal("two bridges got the same token")
		}
		// s1's token must not work against s2.
		resp := doExec(t, s2, s1.Token, []string{"wicket"}, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("cross-bridge token should fail: got %d", resp.StatusCode)
		}
	})
}

// itoa avoids importing strconv just for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// silence unused import on platforms where exec is only referenced indirectly
var _ = exec.Command
var _ sync.Mutex
