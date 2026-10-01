//go:build msbembed && darwin && amd64

package msbembed

// darwin/amd64 has no upstream microsandbox release assets (the project
// publishes aarch64 macOS builds only), so the tagged build keeps the
// host-msb behavior: Available() is false and the ClassifyMsbLaunch gate
// rules launches, exactly like the untagged stub.

var embeddedMsb []byte
var embeddedLibkrunfw []byte

const (
	embeddedMsbName       = ""
	embeddedLibkrunfwName = ""
	extractedLibName      = ""
)

func available() bool { return false }
