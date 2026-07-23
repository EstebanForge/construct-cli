package hostexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EstebanForge/construct-cli/internal/templates"
)

// withShimLinked copies the shim to a temp dir and symlinks `name` at it,
// so the shim's basename() logic resolves to `name`. Returns the symlink path.
func withShimLinked(t *testing.T, name string) (linkPath, shimPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shim is bash + unix-only")
	}
	dir := t.TempDir()
	shimPath = filepath.Join(dir, "construct-host-exec")
	src := []byte(templates.ConstructHostExec)
	if err := os.WriteFile(shimPath, src, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath = filepath.Join(dir, name)
	if err := os.Symlink(shimPath, linkPath); err != nil {
		t.Fatal(err)
	}
	return linkPath, shimPath
}

// runShim invokes the linked shim with env wired to a stub bridge, returning
// stdout, stderr and exit code.
func runShim(t *testing.T, linkPath string, env map[string]string, args []string, stdin []byte) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, linkPath, args...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(string(stdin))
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	// PATH must include jq (host has it via brew) and bash.
	if pathEnv := os.Getenv("PATH"); pathEnv != "" {
		cmd.Env = append(cmd.Env, "PATH="+pathEnv)
	} else {
		cmd.Env = append(cmd.Env, "PATH=/usr/local/bin:/usr/bin:/bin")
	}
	cmd.Env = append(cmd.Env, "HOME="+os.Getenv("HOME"))
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run shim: %v", err)
		}
	}
	return out.String(), errb.String(), code
}

// stubBridge returns an httptest server that mirrors the real bridge's JSONL
// streaming contract, emitting canned frames.
func stubBridge(t *testing.T, token string, frames []frame) *httptest.Server {
	t.Helper()
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(tokenHeader) != token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		flusher, _ := w.(http.Flusher)
		for _, f := range frames {
			b, _ := json.Marshal(f)
			b = append(b, '\n')
			_, _ = w.Write(b)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

func TestShimStreamsStdoutAndExits(t *testing.T) {
	linkPath, _ := withShimLinked(t, "wicket")
	frames := []frame{
		{Type: "stdout", Data: base64.StdEncoding.EncodeToString([]byte("hello from host\n"))},
		{Type: "exit", Code: intPtr(0)},
	}
	srv := stubBridge(t, "tok", frames)
	out, errb, code := runShim(t, linkPath, map[string]string{
		"CONSTRUCT_HOST_EXEC_URL":   srv.URL,
		"CONSTRUCT_HOST_EXEC_TOKEN": "tok",
	}, []string{"--version"}, nil)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb)
	}
	if out != "hello from host\n" {
		t.Fatalf("stdout=%q", out)
	}
}

func TestShimRoutesStderrToStderr(t *testing.T) {
	linkPath, _ := withShimLinked(t, "wicket")
	frames := []frame{
		{Type: "stderr", Data: base64.StdEncoding.EncodeToString([]byte("warning\n"))},
		{Type: "stdout", Data: base64.StdEncoding.EncodeToString([]byte("out\n"))},
		{Type: "exit", Code: intPtr(3)},
	}
	srv := stubBridge(t, "tok", frames)
	out, errb, code := runShim(t, linkPath, map[string]string{
		"CONSTRUCT_HOST_EXEC_URL":   srv.URL,
		"CONSTRUCT_HOST_EXEC_TOKEN": "tok",
	}, nil, nil)
	if code != 3 {
		t.Fatalf("code=%d want 3", code)
	}
	if out != "out\n" {
		t.Fatalf("stdout=%q", out)
	}
	if errb != "warning\n" {
		t.Fatalf("stderr=%q want warning\\n", errb)
	}
}

func TestShimArgvJSONSurvivesQuotesAndSpaces(t *testing.T) {
	linkPath, _ := withShimLinked(t, "wicket")
	// Capture the POST body to assert argv encoding is intact.
	var gotBody execRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		exit := 0
		frame := frame{Type: "exit", Code: &exit}
		b, _ := json.Marshal(frame)
		b = append(b, '\n')
		_, _ = w.Write(b)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, _, code := runShim(t, linkPath, map[string]string{
		"CONSTRUCT_HOST_EXEC_URL":   srv.URL,
		"CONSTRUCT_HOST_EXEC_TOKEN": "tok",
	}, []string{`arg with spaces`, `quotes"and'more`, `unicode: ☕`, "newline\nin arg"}, nil)
	if code != 0 {
		t.Fatal("shim failed")
	}
	// gotBody.Argv[0] == "wicket" (basename), rest == args verbatim
	want := []string{"wicket", `arg with spaces`, `quotes"and'more`, `unicode: ☕`, "newline\nin arg"}
	if len(gotBody.Argv) != len(want) {
		t.Fatalf("argv len=%d want %d (%v)", len(gotBody.Argv), len(want), gotBody.Argv)
	}
	for i := range want {
		if gotBody.Argv[i] != want[i] {
			t.Fatalf("argv[%d]=%q want %q", i, gotBody.Argv[i], want[i])
		}
	}
}

func TestShimPassesStdinBase64(t *testing.T) {
	linkPath, _ := withShimLinked(t, "wicket")
	var gotBody execRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		exit := 0
		frame := frame{Type: "exit", Code: &exit}
		b, _ := json.Marshal(frame)
		b = append(b, '\n')
		_, _ = w.Write(b)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, _, code := runShim(t, linkPath, map[string]string{
		"CONSTRUCT_HOST_EXEC_URL":   srv.URL,
		"CONSTRUCT_HOST_EXEC_TOKEN": "tok",
	}, nil, []byte("piped bytes\nsecond line"))
	if code != 0 {
		t.Fatal("shim failed")
	}
	decoded, err := base64.StdEncoding.DecodeString(gotBody.Stdin)
	if err != nil {
		t.Fatalf("stdin b64 decode: %v", err)
	}
	if string(decoded) != "piped bytes\nsecond line" {
		t.Fatalf("stdin=%q", decoded)
	}
}

func TestShimExits126WhenURLUnset(t *testing.T) {
	linkPath, _ := withShimLinked(t, "wicket")
	// No URL/TOKEN env at all.
	out, errb, code := runShim(t, linkPath, nil, nil, nil)
	if code != 126 {
		t.Fatalf("code=%d want 126", code)
	}
	if out != "" {
		t.Fatalf("stdout should be empty: %q", out)
	}
	if !strings.Contains(errb, "not configured") {
		t.Fatalf("stderr should mention misconfig: %q", errb)
	}
}

func TestShimExits126WhenUnreachable(t *testing.T) {
	linkPath, _ := withShimLinked(t, "wicket")
	// Point at a TCP port that refuses connections. Use an ephemeral bind then
	// close it to guarantee refusal.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	_, errb, code := runShim(t, linkPath, map[string]string{
		"CONSTRUCT_HOST_EXEC_URL":   "http://" + addr,
		"CONSTRUCT_HOST_EXEC_TOKEN": "tok",
	}, nil, nil)
	if code != 126 {
		t.Fatalf("code=%d want 126 (stderr=%s)", code, errb)
	}
	if !strings.Contains(errb, "unreachable") {
		t.Fatalf("stderr should mention unreachable: %q", errb)
	}
}

func TestShimStreamsCarriageReturnProgress(t *testing.T) {
	// \r progress bars produce no \n for a while; the bridge frames each pipe
	// Read separately, so the shim must handle a frame whose decoded bytes
	// contain \r (and no trailing \n).
	linkPath, _ := withShimLinked(t, "wicket")
	progress := "working... \rdone\n"
	frames := []frame{
		{Type: "stdout", Data: base64.StdEncoding.EncodeToString([]byte(progress))},
		{Type: "exit", Code: intPtr(0)},
	}
	srv := stubBridge(t, "tok", frames)
	out, _, code := runShim(t, linkPath, map[string]string{
		"CONSTRUCT_HOST_EXEC_URL":   srv.URL,
		"CONSTRUCT_HOST_EXEC_TOKEN": "tok",
	}, nil, nil)
	if code != 0 {
		t.Fatal("non-zero exit")
	}
	if out != progress {
		t.Fatalf("stdout=%q want %q", out, progress)
	}
}

func TestShimDoesNotDeadlockOnOpenEmptyPipe(t *testing.T) {
	// Regression for the pi-unified-exec deadlock: when stdin is an open pipe
	// that never sends data and never closes, the old bare `base64` read
	// blocked forever (no EOF). This launches the shim with stdin wired to a
	// pipe whose write end we keep open for the whole run and asserts it
	// returns quickly (the non-blocking peek must skip the read entirely) with
	// the bridge's exit code instead of hanging.
	//
	// The previous bounded-read fix (timeout 5) would have passed this too, but
	// only after waiting the full 5s timeout on every run — the hybrid peek
	// fix must return near-instantly, which the <3s assertion below enforces.
	linkPath, _ := withShimLinked(t, "wicket")
	frames := []frame{
		{Type: "stdout", Data: base64.StdEncoding.EncodeToString([]byte("ok\n"))},
		{Type: "exit", Code: intPtr(0)},
	}
	srv := stubBridge(t, "tok", frames)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Keep the write end open for the whole test so the pipe never sends EOF
	// while the shim is running — mirroring an interactive launcher that holds
	// stdin open. Direct cleanup, no goroutine needed.
	t.Cleanup(func() { w.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, linkPath, "--version")
	cmd.Stdin = r
	cmd.Env = []string{
		"CONSTRUCT_HOST_EXEC_URL=" + srv.URL,
		"CONSTRUCT_HOST_EXEC_TOKEN=tok",
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	var out strings.Builder
	cmd.Stdout = &out

	start := time.Now()
	err = cmd.Run()
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Fatalf("shim took %s on open-empty stdin; non-blocking peek should skip the read", elapsed)
	}
	if err != nil {
		t.Fatalf("shim failed after %s: %v out=%q", elapsed, err, out.String())
	}
	if out.String() != "ok\n" {
		t.Fatalf("stdout=%q want ok\n", out.String())
	}
}

func TestShimWarnsOnStdinOverByteCap(t *testing.T) {
	// The 1 MiB byte cap truncates silently by design; the shim MUST emit a
	// stderr warning so truncation is attributable to the shim, not surfaced
	// later as a confusing bridge-side parse error.
	linkPath, _ := withShimLinked(t, "wicket")
	frames := []frame{
		{Type: "exit", Code: intPtr(0)},
	}
	srv := stubBridge(t, "tok", frames)

	// 2 MiB of 'x' — well over the 1 MiB cap.
	oversized := strings.Repeat("x", 2*1024*1024)

	_, errb, code := runShim(t, linkPath, map[string]string{
		"CONSTRUCT_HOST_EXEC_URL":   srv.URL,
		"CONSTRUCT_HOST_EXEC_TOKEN": "tok",
	}, nil, []byte(oversized))
	if code != 0 {
		t.Fatalf("code=%d want 0 (truncation is not fatal) stderr=%s", code, errb)
	}
	if !strings.Contains(errb, "truncated") {
		t.Fatalf("stderr should warn about truncation: %q", errb)
	}
}

func intPtr(i int) *int { return &i }

// silence unused (fmt/io imported for future use in this harness)
var _ = fmt.Sprintf
var _ = io.EOF
