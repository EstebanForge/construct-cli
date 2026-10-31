//go:build acceptance

// Package acceptance drives the real construct binary through txtar
// scenario scripts, each in an isolated HOME. Run with:
//
//	go test -tags acceptance ./acceptance
package acceptance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// binDir holds the once-per-run binary build, prepended to every script's
// PATH so scenarios exercise the compiled artifact, not in-process code.
// CONSTRUCT_ACCEPTANCE_COVER=1 builds with -cover and reports subprocess
// coverage after the suite.
var (
	binDir   string
	coverDir string
)

func TestMain(m *testing.M) {
	_ = os.Setenv("CONSTRUCT_NO_START_RUNTIME", "1")
	var err error
	binDir, err = os.MkdirTemp("", "construct-acceptance")
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance: temp dir:", err)
		os.Exit(1)
	}
	buildArgs := []string{"build"}
	if os.Getenv("CONSTRUCT_ACCEPTANCE_COVER") == "1" {
		coverDir = filepath.Join(binDir, "cover")
		if err := os.MkdirAll(coverDir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "acceptance: cover dir:", err)
			os.Exit(1)
		}
		buildArgs = append(buildArgs, "-cover")
	}
	buildArgs = append(buildArgs, "-o", filepath.Join(binDir, "construct"), "./cmd/construct")
	build := exec.Command("go", buildArgs...)
	// go test runs with the package dir as cwd; build from the module root.
	modOut, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance: go env GOMOD:", err)
		os.Exit(1)
	}
	build.Dir = filepath.Dir(strings.TrimSpace(string(modOut)))
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(binDir)
		fmt.Fprintln(os.Stderr, "acceptance: build:", err)
		os.Exit(1)
	}
	code := m.Run()
	if coverDir != "" {
		report := exec.Command("go", "tool", "covdata", "percent", "-i", coverDir)
		report.Stdout = os.Stdout
		report.Stderr = os.Stderr
		_ = report.Run()
	}
	os.RemoveAll(binDir)
	os.Exit(code)
}

func TestCLI(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata",
		Setup: func(e *testscript.Env) error {
			// testscript defaults HOME to /no-home; construct derives its
			// config dir from HOME, so every scenario gets an isolated one.
			setVar(&e.Vars, "HOME", e.WorkDir)
			setVar(&e.Vars, "PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if coverDir != "" {
				setVar(&e.Vars, "GOCOVERDIR", coverDir)
			}
			return nil
		},
	})
}

func setVar(vars *[]string, key, value string) {
	for i, kv := range *vars {
		if strings.HasPrefix(kv, key+"=") {
			(*vars)[i] = key + "=" + value
			return
		}
	}
	*vars = append(*vars, key+"="+value)
}
