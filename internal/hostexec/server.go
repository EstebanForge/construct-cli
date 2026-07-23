package hostexec

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/EstebanForge/construct-cli/internal/ui"
)

// DefaultTimeout caps a single host exec invocation. Finite (not unbounded)
// so Stop()'s wg.Wait() cannot block teardown forever on a hung host binary.
// Override per-invocation via CONSTRUCT_HOST_EXEC_TIMEOUT (seconds); 0 falls
// back to this default.
const DefaultTimeout = 30 * time.Minute

// exitCodeKill is synthesized as the exit chunk's code when the child is killed
// by the bridge (timeout or client disconnect), mirroring the timeout(1)
// convention. The shim treats it as a normal exit code.
const exitCodeKill = 124

// tokenHeader is the bearer-header a request must carry to authenticate.
const tokenHeader = "X-Construct-Exec-Token"

// Server is the host-side exec bridge: an HTTP server that runs a fixed,
// allowlisted set of host binaries on behalf of the container and streams
// their stdout/stderr back as line-framed JSON.
type Server struct {
	Port     int
	Token    string
	URL      string
	timeout  time.Duration
	resolved map[string]string // name -> absolute host path
	listener net.Listener
	wg       sync.WaitGroup
	writeMu  sync.Mutex // serializes ResponseWriter frame writes
}

// execRequest is the JSON body the shim POSTs.
type execRequest struct {
	Argv  []string `json:"argv"`
	Stdin string   `json:"stdin"` // base64-encoded bytes
}

// frame is one streamed JSONL line.
type frame struct {
	Type string `json:"type"`           // stdout | stderr | exit
	Data string `json:"data,omitempty"` // base64 (stdout/stderr)
	Code *int   `json:"code,omitempty"` // exit only
}

// StartServer resolves each binary on the host, binds a random port, and starts
// serving in a background goroutine. host is the hostname the container will
// use to reach us (typically host.docker.internal). binaries may be empty
// (in which case the allowlist accepts nothing and the bridge is useless; the
// caller normally guards StartServer on len(binaries) > 0).
//
// bindAddr follows the SSH bridge split: macOS binds loopback (Docker Desktop
// routes host.docker.internal there), Linux binds 0.0.0.0 so the container can
// reach us. The per-session token (D4) is what makes the 0.0.0.0 bind safe.
func StartServer(host string, binaries []string, timeout time.Duration) (*Server, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	// Resolve every binary up front via LookPath. Fail closed: a missing entry
	// is a config error, not a silent runtime miss later. (D2: resolve-once
	// avoids per-request PATH-poisoning / TOCTOU.)
	resolved := make(map[string]string, len(binaries))
	for _, name := range binaries {
		if name == "" {
			continue
		}
		if _, ok := resolved[name]; ok {
			continue
		}
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("hostexec: host binary %q not found on PATH: %w", name, err)
		}
		resolved[name] = path
	}

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("hostexec: generate token: %w", err)
	}

	bindAddr := "127.0.0.1:0"
	if runtime.GOOS == "linux" {
		bindAddr = "0.0.0.0:0"
	}
	listener, err := net.Listen("tcp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("hostexec: listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	if host == "" {
		host = "host.docker.internal"
	}

	s := &Server{
		Port:     port,
		Token:    token,
		URL:      fmt.Sprintf("http://%s:%d", host, port),
		timeout:  timeout,
		resolved: resolved,
		listener: listener,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/exec", s.handleExec)

	srv := &http.Server{Handler: mux}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := srv.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			logf("[hostexec] serve error: %v", err)
		}
	}()

	logf("[hostexec] bridge started url=%s allowlist=%d timeout=%s", s.URL, len(resolved), timeout)
	return s, nil
}

// Stop closes the listener and waits for in-flight requests. Each request's
// context is derived from the request and bounded by s.timeout, so this wait
// is itself bounded; in-flight children are killed via their process group.
func (s *Server) Stop() {
	if s == nil {
		return
	}
	if s.listener != nil {
		_ = s.listener.Close() //nolint:errcheck
	}
	s.wg.Wait()
}

// handleExec authenticates, validates argv[0] against the allowlist, runs the
// resolved absolute path, and streams stdout/stderr as JSONL frames followed
// by an exit frame. The child runs in its own process group so timeout /
// disconnect kills can reach its descendants.
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(tokenHeader) != s.Token {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req execRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<20)) // sane cap on stdin
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Argv) == 0 {
		http.Error(w, "argv empty", http.StatusBadRequest)
		return
	}
	name := req.Argv[0]
	absPath, ok := s.resolved[name]
	if !ok {
		// Fail closed: unknown argv[0] is never proxied.
		http.Error(w, "binary not allowlisted", http.StatusForbidden)
		return
	}

	stdinBytes, err := base64.StdEncoding.DecodeString(req.Stdin)
	if err != nil {
		http.Error(w, "stdin not base64: "+err.Error(), http.StatusBadRequest)
		return
	}

	// One context covers both timeout and client disconnect (r.Context() is
	// canceled when the HTTP connection drops). Either path kills the child.
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, absPath, req.Argv[1:]...)
	// Own process group so kill(-pgid) reaches descendants.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Wire stdin from the (fully-buffered) bytes the shim sent up front.
	cmd.Stdin = bytesReader(stdinBytes)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "stdout pipe: "+err.Error(), http.StatusInternalServerError)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		http.Error(w, "stderr pipe: "+err.Error(), http.StatusInternalServerError)
		return
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	pgid := cmd.Process.Pid // == process group id because Setpgid

	// If ctx fires (timeout or disconnect), kill the whole group. Normal exit
	// races this via doneCh; whichever wins, the other is a no-op.
	doneCh := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Negative pid => signal the whole process group.
			_ = syscall.Kill(-pgid, syscall.SIGKILL) //nolint:errcheck
		case <-doneCh:
		}
	}()

	flusher, _ := w.(http.Flusher)
	writeFrame := func(f frame) {
		b, _ := json.Marshal(f) //nolint:errcheck
		b = append(b, '\n')
		// Serialize across the two drain goroutines so JSONL lines are never
		// interleaved mid-write.
		s.writeMu.Lock()
		_, werr := w.Write(b)
		if flusher != nil {
			flusher.Flush()
		}
		s.writeMu.Unlock()
		if werr != nil {
			// Connection gone; further writes are wasted but harmless.
			ui.LogDebug("hostexec: write frame: %v", werr)
		}
	}

	// Drain stdout and stderr concurrently into the stream. Each pipe Read is
	// its own frame (binary-safe via base64; handles \r progress bars etc.).
	var drainWg sync.WaitGroup
	drain := func(rc io.ReadCloser, ftype string) {
		defer drainWg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, rerr := rc.Read(buf)
			if n > 0 {
				writeFrame(frame{Type: ftype, Data: base64.StdEncoding.EncodeToString(buf[:n])})
			}
			if rerr != nil {
				return
			}
		}
	}
	drainWg.Add(2)
	go drain(stdout, "stdout")
	go drain(stderr, "stderr")

	// Drain both pipes to EOF BEFORE cmd.Wait(). os/exec closes the pipe FDs
	// during Wait; a Read racing that close returns "file already closed"
	// mid-buffer and drops output. The StdoutPipe docs mandate this order
	// ("incorrect to call Wait before all reads from the pipe have completed").
	// Safe against deadlocks: drains consume output as the child produces it,
	// and ctx's kill(-pgid) closes the write ends on timeout/disconnect.
	drainWg.Wait()
	werr := cmd.Wait()
	close(doneCh)

	// Determine exit code. A ctx error (timeout/disconnect) => synthesized 124
	// so the shim/agent see a recognizable code; otherwise the real status.
	code := 0
	switch {
	case ctx.Err() != nil:
		c := exitCodeKill
		code = c
	case werr != nil:
		if exitErr, ok := werr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			ui.LogDebug("hostexec: wait error: %v", werr)
			code = 1
		}
	}

	ec := code
	writeFrame(frame{Type: "exit", Code: &ec})

	logf("[hostexec] exec argv=%v path=%s code=%d dur=%s", req.Argv, absPath, code, time.Since(start).Round(time.Millisecond))
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// logf appends to ~/.config/construct-cli/logs/host_exec.log (always-on audit
// trail, mirroring clipboard_server.log).
func logf(format string, args ...any) {
	logDir := os.Getenv("HOME") + "/.config/construct-cli/logs"
	_ = os.MkdirAll(logDir, 0o755) //nolint:errcheck
	f, err := os.OpenFile(logDir+"/host_exec.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck
	ts := time.Now().Format("2006-01-02 15:04:05")
	_, _ = fmt.Fprintf(f, "[%s] ", ts)     //nolint:errcheck
	_, _ = fmt.Fprintf(f, format, args...) //nolint:errcheck
	if n := len(format); n > 0 && format[n-1] != '\n' {
		_, _ = fmt.Fprintln(f) //nolint:errcheck
	}
}

// bytesReader returns a non-nil io.Reader even for empty input, so cmd.Stdin
// is never nil (a nil Stdin would connect the child to /dev/null, which is
// also fine, but being explicit avoids surprises for binaries that fstat
// stdin).
func bytesReader(b []byte) io.Reader {
	return &bytesReaderImpl{b: b}
}

type bytesReaderImpl struct {
	b   []byte
	off int
}

func (r *bytesReaderImpl) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}
