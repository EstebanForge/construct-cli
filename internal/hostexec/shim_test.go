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

func intPtr(i int) *int { return &i }

// silence unused (fmt/io imported for future use in this harness)
var _ = fmt.Sprintf
var _ = io.EOF
