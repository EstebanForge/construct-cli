//go:build !msbembed

package msbembed

// Stub variant: builds without -tags msbembed carry no runtime pair and
// keep the pre-embed behavior (host msb + ClassifyMsbLaunch gate).

var embeddedMsb []byte
var embeddedLibkrunfw []byte

const (
	embeddedMsbName       = ""
	embeddedLibkrunfwName = ""
	extractedLibName      = ""
)

func available() bool { return false }
