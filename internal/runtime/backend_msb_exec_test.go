package runtime

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// fakeEventSource scripts Recv results for msbDrain tests: one step per
// call, io.EOF when the script runs out (a real stream ends, it never
// blocks).
type fakeEventSource struct {
	steps []fakeStep
	i     int
	kills int
}

type fakeStep struct {
	ev  *msb.ExecEvent
	err error
}

func (f *fakeEventSource) Recv(_ context.Context) (*msb.ExecEvent, error) {
	if f.i >= len(f.steps) {
		return nil, io.EOF
	}
	step := f.steps[f.i]
	f.i++
	return step.ev, step.err
}

func (f *fakeEventSource) Kill(_ context.Context) error {
	f.kills++
	return nil
}

func evStdout(s string) *msb.ExecEvent {
	return &msb.ExecEvent{Kind: msb.ExecEventStdout, Data: []byte(s)}
}

func evStderr(s string) *msb.ExecEvent {
	return &msb.ExecEvent{Kind: msb.ExecEventStderr, Data: []byte(s)}
}

func evExited(code int) *msb.ExecEvent {
	return &msb.ExecEvent{Kind: msb.ExecEventExited, ExitCode: code}
}

func evStdinError(msg string) *msb.ExecEvent {
	return &msb.ExecEvent{Kind: msb.ExecEventStdinError, Failure: &msb.ExecFailure{Message: msg}}
}

// TestMsbDrainStdinErrorKeepsDraining pins the StdinError semantics: a
// host-side stdin pump failure is not the process ending — the guest
// still exits with a real code, so drain continues to the exit event
// instead of reporting exit 1 and losing it.
func TestMsbDrainStdinErrorKeepsDraining(t *testing.T) {
	src := &fakeEventSource{steps: []fakeStep{
		{ev: evStdout("partial")}, {ev: evStdinError("broken pipe")}, {ev: evExited(7)},
	}}
	var out, errBuf bytes.Buffer
	code, err := msbDrain(context.Background(), src, &out, &errBuf)
	if err != nil {
		t.Fatalf("drain returned error: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code %d, want the real guest code 7", code)
	}
	if out.String() != "partial" {
		t.Fatalf("stdout %q, want %q", out.String(), "partial")
	}
}

func TestMsbDrainRoutesStreamsAndExit(t *testing.T) {
	src := &fakeEventSource{steps: []fakeStep{
		{ev: evStdout("o1")}, {ev: evStderr("e1")}, {ev: evStdout("o2")}, {ev: evExited(3)},
	}}
	var out, errBuf bytes.Buffer
	code, err := msbDrain(context.Background(), src, &out, &errBuf)
	if err != nil || code != 3 {
		t.Fatalf("drain: code=%d err=%v, want 3/nil", code, err)
	}
	if out.String() != "o1o2" || errBuf.String() != "e1" {
		t.Fatalf("routing broken: out=%q err=%q", out.String(), errBuf.String())
	}
}

func TestMsbDrainCanceledKillsThenReportsExit(t *testing.T) {
	// A real Recv on a canceled ctx returns the ctx error, then the
	// post-kill drain on the background ctx delivers the exit event.
	src := &fakeEventSource{steps: []fakeStep{
		{err: context.Canceled}, {ev: evExited(9)},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, err := msbDrain(ctx, src, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("drain after cancel: %v", err)
	}
	if code != 9 {
		t.Fatalf("exit code %d, want 9 from the post-kill drain", code)
	}
	if src.kills != 1 {
		t.Fatalf("kills=%d, want the guest process killed once", src.kills)
	}
}

func TestMsbDrainFailedEventCarriesMessage(t *testing.T) {
	src := &fakeEventSource{steps: []fakeStep{
		{ev: &msb.ExecEvent{Kind: msb.ExecEventFailed, Failure: &msb.ExecFailure{Message: "exec: no such file"}}},
	}}
	_, err := msbDrain(context.Background(), src, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exec: no such file") {
		t.Fatalf("error %v, want the failure message", err)
	}
}
