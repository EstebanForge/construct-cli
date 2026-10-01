//go:build msbembed && darwin && arm64

package msbembed

import _ "embed"

// Embedded runtime pair for darwin/arm64, placed by
// scripts/embed-msb-runtime.sh from the upstream microsandbox release
// pinned to the go.mod SDK version.

//go:embed assets/msb-darwin-aarch64
var embeddedMsb []byte

//go:embed assets/libkrunfw-darwin-aarch64.dylib
var embeddedLibkrunfw []byte

const (
	embeddedMsbName       = "msb-darwin-aarch64"
	embeddedLibkrunfwName = "libkrunfw-darwin-aarch64.dylib"
	extractedLibName      = "libkrunfw.dylib"
)

func available() bool { return true }
