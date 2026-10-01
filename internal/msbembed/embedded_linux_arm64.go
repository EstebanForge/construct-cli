//go:build msbembed && linux && arm64

package msbembed

import _ "embed"

// Embedded runtime pair for linux/arm64, placed by
// scripts/embed-msb-runtime.sh from the upstream microsandbox release
// pinned to the go.mod SDK version.

//go:embed assets/msb-linux-aarch64
var embeddedMsb []byte

//go:embed assets/libkrunfw-linux-aarch64.so
var embeddedLibkrunfw []byte

const (
	embeddedMsbName       = "msb-linux-aarch64"
	embeddedLibkrunfwName = "libkrunfw-linux-aarch64.so"
	extractedLibName      = "libkrunfw.so"
)

func available() bool { return true }
