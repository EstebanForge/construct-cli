//go:build msbembed && linux && amd64

package msbembed

import _ "embed"

// Embedded runtime pair for linux/amd64, placed by
// scripts/embed-msb-runtime.sh from the upstream microsandbox release
// pinned to the go.mod SDK version.

//go:embed assets/msb-linux-x86_64
var embeddedMsb []byte

//go:embed assets/libkrunfw-linux-x86_64.so
var embeddedLibkrunfw []byte

const (
	embeddedMsbName       = "msb-linux-x86_64"
	embeddedLibkrunfwName = "libkrunfw-linux-x86_64.so"
	extractedLibName      = "libkrunfw.so"
)

func available() bool { return true }
