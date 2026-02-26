package ui

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestGumAvailableFalseWhenNonTTY(t *testing.T) {
	stdinFile := mustTempFile(t)
	stdoutFile := mustTempFile(t)
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinFile)
	defer closeQuietly(stdoutFile)
	defer closeQuietly(stderrFile)

	withStdio(t, stdinFile, stdoutFile, stderrFile, func() {
		if GumAvailable() {
			t.Fatalf("expected GumAvailable to be false in non-TTY environment")
		}
	})
}

func TestGetGumCommandUsesEmbeddedShim(t *testing.T) {
	cmd := GetGumCommand("style", "hello")
	if len(cmd.Args) < 3 {
		t.Fatalf("expected at least 3 args, got %v", cmd.Args)
	}
	if cmd.Args[1] != "__gum" {
		t.Fatalf("expected embedded gum shim invocation, got args %v", cmd.Args)
	}
	if cmd.Args[2] != "style" {
		t.Fatalf("expected gum subcommand style, got args %v", cmd.Args)
	}
}

func TestRunEmbeddedGumStyleInNonTTY(t *testing.T) {
	stdinFile := mustTempFile(t)
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinFile)
	defer closeQuietly(stderrFile)

	output, code := captureEmbeddedRun(t, stdinFile, stderrFile, []string{"style", "--foreground", "242", "hello"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; output=%q", code, output)
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("expected styled output to contain input text, got %q", output)
	}
}

func TestRunEmbeddedGumFormatInNonTTY(t *testing.T) {
	stdinFile := mustTempFile(t)
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinFile)
	defer closeQuietly(stderrFile)

	output, code := captureEmbeddedRun(t, stdinFile, stderrFile, []string{"format", "hello"})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; output=%q", code, output)
	}
	if !strings.Contains(strings.ToLower(output), "hello") {
		t.Fatalf("expected formatted output to contain input text, got %q", output)
	}
}

func TestRunEmbeddedGumConfirmFromPipedInput(t *testing.T) {
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinReader)
	defer closeQuietly(stderrFile)

	if _, err := stdinWriter.WriteString("y\n"); err != nil {
		t.Fatalf("failed writing confirmation input: %v", err)
	}
	closeQuietly(stdinWriter)

	_, code := captureEmbeddedRun(t, stdinReader, stderrFile, []string{"confirm", "Proceed?"})
	if code != 0 {
		t.Fatalf("expected confirm to accept piped yes input, got code %d", code)
	}
}

func TestRunEmbeddedGumConfirmRejectsNoFromPipe(t *testing.T) {
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinReader)
	defer closeQuietly(stderrFile)

	if _, err := stdinWriter.WriteString("n\n"); err != nil {
		t.Fatalf("failed writing confirmation input: %v", err)
	}
	closeQuietly(stdinWriter)

	_, code := captureEmbeddedRun(t, stdinReader, stderrFile, []string{"confirm", "Proceed?"})
	if code != 1 {
		t.Fatalf("expected confirm to reject piped no input with code 1, got %d", code)
	}
}

func TestRunEmbeddedGumUnsupportedCommand(t *testing.T) {
	stdinFile := mustTempFile(t)
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinFile)
	defer closeQuietly(stderrFile)

	_, code := captureEmbeddedRun(t, stdinFile, stderrFile, []string{"unsupported-cmd"})
	if code == 0 {
		t.Fatalf("expected unsupported command to fail")
	}
}

func TestGumSpinnerFallsBackInNonTTY(t *testing.T) {
	stdinFile := mustTempFile(t)
	stdoutFile := mustTempFile(t)
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinFile)
	defer closeQuietly(stdoutFile)
	defer closeQuietly(stderrFile)

	withStdio(t, stdinFile, stdoutFile, stderrFile, func() {
		out := GumSpinner("loading", func() []string {
			return []string{"ok"}
		})
		if len(out) != 1 || out[0] != "ok" {
			t.Fatalf("unexpected spinner result: %v", out)
		}
	})

	if _, err := stdoutFile.Seek(0, 0); err != nil {
		t.Fatalf("failed to rewind stdout temp file: %v", err)
	}
	data, err := io.ReadAll(stdoutFile)
	if err != nil {
		t.Fatalf("failed to read stdout temp file: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("expected no spinner output in non-TTY fallback, got %q", string(data))
	}
}

func TestShouldUsePlainConfirmForSSHSession(t *testing.T) {
	t.Setenv("CONSTRUCT_FORCE_GUM_CONFIRM", "")
	t.Setenv("CONSTRUCT_PLAIN_CONFIRM", "")
	t.Setenv("SSH_CONNECTION", "10.0.0.1 12345 10.0.0.2 22")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")

	if !shouldUsePlainConfirm() {
		t.Fatal("expected plain confirm in SSH session")
	}
}

func TestShouldUsePlainConfirmForceGumOverridesSSH(t *testing.T) {
	t.Setenv("CONSTRUCT_FORCE_GUM_CONFIRM", "1")
	t.Setenv("CONSTRUCT_PLAIN_CONFIRM", "")
	t.Setenv("SSH_CONNECTION", "10.0.0.1 12345 10.0.0.2 22")

	if shouldUsePlainConfirm() {
		t.Fatal("expected gum confirm when CONSTRUCT_FORCE_GUM_CONFIRM=1")
	}
}

func TestPlainConfirmDefaultsToYesOnEmptyInput(t *testing.T) {
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	stdoutFile := mustTempFile(t)
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinReader)
	defer closeQuietly(stdoutFile)
	defer closeQuietly(stderrFile)

	if _, err := stdinWriter.WriteString("\n"); err != nil {
		t.Fatalf("failed writing confirmation input: %v", err)
	}
	closeQuietly(stdinWriter)

	var confirmed bool
	withStdio(t, stdinReader, stdoutFile, stderrFile, func() {
		confirmed = plainConfirm("Proceed?", true)
	})
	if !confirmed {
		t.Fatal("expected empty input to accept default yes")
	}
}

func TestPlainConfirmParsesNoInput(t *testing.T) {
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	stdoutFile := mustTempFile(t)
	stderrFile := mustTempFile(t)
	defer closeQuietly(stdinReader)
	defer closeQuietly(stdoutFile)
	defer closeQuietly(stderrFile)

	if _, err := stdinWriter.WriteString("n\n"); err != nil {
		t.Fatalf("failed writing confirmation input: %v", err)
	}
	closeQuietly(stdinWriter)

	var confirmed bool
	withStdio(t, stdinReader, stdoutFile, stderrFile, func() {
		confirmed = plainConfirm("Proceed?", true)
	})
	if confirmed {
		t.Fatal("expected 'n' input to reject confirmation")
	}
}

func captureEmbeddedRun(t *testing.T, stdinFile, stderrFile *os.File, args []string) (string, int) {
	t.Helper()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	defer closeQuietly(stdoutReader)

	oldIn := os.Stdin
	oldOut := os.Stdout
	oldErr := os.Stderr
	os.Stdin = stdinFile
	os.Stdout = stdoutWriter
	os.Stderr = stderrFile

	code := RunEmbeddedGum(args)

	closeQuietly(stdoutWriter)
	os.Stdin = oldIn
	os.Stdout = oldOut
	os.Stderr = oldErr

	data, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("failed to read stdout output: %v", err)
	}
	return string(data), code
}

func withStdio(t *testing.T, stdinFile, stdoutFile, stderrFile *os.File, fn func()) {
	t.Helper()
	oldIn := os.Stdin
	oldOut := os.Stdout
	oldErr := os.Stderr
	os.Stdin = stdinFile
	os.Stdout = stdoutFile
	os.Stderr = stderrFile
	defer func() {
		os.Stdin = oldIn
		os.Stdout = oldOut
		os.Stderr = oldErr
	}()

	fn()
}

func mustTempFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "ui-test-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	return file
}

func closeQuietly(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}
