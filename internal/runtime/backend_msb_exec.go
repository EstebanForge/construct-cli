package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// msbExecCommon assembles the shared SDK exec options from ExecOptions.
func msbExecCommon(opts ExecOptions) []msb.ExecOption {
	var eopts []msb.ExecOption
	if opts.User != "" {
		eopts = append(eopts, msb.WithExecUser(opts.User))
	}
	if opts.Workdir != "" {
		eopts = append(eopts, msb.WithExecCwd(opts.Workdir))
	}
	if len(opts.Env) > 0 {
		eopts = append(eopts, msb.WithExecEnv(envSliceToMap(opts.Env)))
	}
	return eopts
}

// ExecStream runs a command with streamed stdio (non-interactive): stdout
// and stderr stream to the host streams, no stdin attached. Exit-code
// fidelity: non-zero exits arrive as ExecEventExited, not errors.
func (m *MsbBackend) ExecStream(ctx context.Context, opts ExecOptions) (int, error) {
	if len(opts.Command) == 0 {
		return 1, errors.New("msb exec: empty command")
	}
	sb, err := m.connect(ctx, opts.Name)
	if err != nil {
		return 1, err
	}
	defer func() {
		_ = sb.Close() //nolint:errcheck // best-effort release of the SDK handle
	}()

	h, err := sb.ExecStream(ctx, opts.Command[0], opts.Command[1:], msbExecCommon(opts)...)
	if err != nil {
		return 1, fmt.Errorf("msb exec stream: %w", err)
	}
	defer func() {
		_ = h.Close() //nolint:errcheck // stream already drained
	}()
	return msbDrain(h, os.Stdout, os.Stderr)
}

// ExecInteractive runs a command with full interactive stdio: host stdin
// is piped to the guest, a TTY is allocated when stdin is a terminal (raw
// mode + SIGWINCH resize forwarding), and output streams live. This is the
// msb analog of docker exec -it and the path interactive agents run on.
func (m *MsbBackend) ExecInteractive(ctx context.Context, opts ExecOptions) (int, error) {
	if len(opts.Command) == 0 {
		return 1, errors.New("msb exec: empty command")
	}
	sb, err := m.connect(ctx, opts.Name)
	if err != nil {
		return 1, err
	}
	defer func() {
		_ = sb.Close() //nolint:errcheck // best-effort release of the SDK handle
	}()

	stdTTY := term.IsTerminal(int(os.Stdin.Fd()))
	eopts := append(msbExecCommon(opts), msb.WithExecStdinPipe(), msb.WithExecTTY(stdTTY))
	h, err := sb.ExecStream(ctx, opts.Command[0], opts.Command[1:], eopts...)
	if err != nil {
		return 1, fmt.Errorf("msb exec interactive: %w", err)
	}

	// stdin pump: host stdin -> guest stdin sink. Close on EOF so the guest
	// sees end-of-input (msb output EOF semantics need the write end closed).
	pumpDone := make(chan struct{})
	sink := h.TakeStdin()
	go func() {
		defer close(pumpDone)
		if sink == nil {
			return
		}
		_, _ = io.Copy(sink, os.Stdin) //nolint:errcheck // guest-side EOF is the signal
		_ = sink.Close()               //nolint:errcheck // sink already closed on guest exit
	}()

	// Terminal plumbing: raw mode keeps TUI agents in control of the screen;
	// SIGWINCH forwards window resizes to the guest PTY.
	if stdTTY {
		oldState, rawErr := term.MakeRaw(int(os.Stdin.Fd()))
		if rawErr == nil {
			defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }() //nolint:errcheck // best-effort restore
		}
		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
		go func() {
			for range winch {
				w, h2, err := term.GetSize(int(os.Stdin.Fd()))
				if err != nil {
					continue
				}
				_ = h.Resize(context.Background(), uint16(h2), uint16(w)) //nolint:errcheck // next SIGWINCH retries
			}
		}()
	}

	defer func() {
		_ = h.Close() //nolint:errcheck // stream already drained
	}()
	return msbDrain(h, os.Stdout, os.Stderr)
}

// msbDrain pumps ExecStream events to the host streams until the process
// exits and returns its exit code.
func msbDrain(h *msb.ExecHandle, stdout, stderr io.Writer) (int, error) {
	for {
		ev, err := h.Recv(context.Background())
		if err != nil {
			return 1, fmt.Errorf("msb exec stream: %w", err)
		}
		switch ev.Kind {
		case msb.ExecEventStdout:
			if _, werr := stdout.Write(ev.Data); werr != nil {
				return 1, fmt.Errorf("msb exec stream: stdout write: %w", werr)
			}
		case msb.ExecEventStderr:
			if _, werr := stderr.Write(ev.Data); werr != nil {
				return 1, fmt.Errorf("msb exec stream: stderr write: %w", werr)
			}
		case msb.ExecEventExited:
			return ev.ExitCode, nil
		case msb.ExecEventFailed, msb.ExecEventStdinError:
			msg := "unknown failure"
			if ev.Failure != nil && ev.Failure.Message != "" {
				msg = ev.Failure.Message
			}
			return 1, fmt.Errorf("msb exec stream: %v: %s", ev.Kind, strings.TrimSpace(msg))
		}
	}
}
