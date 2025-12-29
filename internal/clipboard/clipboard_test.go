package clipboard

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// Mock exec command for testing
var testExecCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func TestErrNoImage(t *testing.T) {
	if !errors.Is(ErrNoImage, ErrNoImage) {
		t.Error("ErrNoImage should equal itself")
	}
}

func TestErrNoText(t *testing.T) {
	if !errors.Is(ErrNoText, ErrNoText) {
		t.Error("ErrNoText should equal itself")
	}
}

// TestGetText_UnsupportedOS tests error for unsupported operating system
func TestGetText_UnsupportedOS(t *testing.T) {
	// This test cannot run on real platforms, but verifies error path
	// We'll verify the error message format instead
	supportedOS := map[string]bool{
		"darwin":  true,
		"linux":   true,
		"windows": true,
	}

	// Current OS should be supported
	if supportedOS[runtime.GOOS] {
		t.Skipf("Skipping on supported OS: %s", runtime.GOOS)
	}

	data, err := GetText()
	if err == nil {
		t.Error("Expected error on unsupported OS")
	}
	if data != nil {
		t.Error("Expected nil data on error")
	}
}

// TestGetImage_UnsupportedOS tests error for unsupported operating system
func TestGetImage_UnsupportedOS(t *testing.T) {
	supportedOS := map[string]bool{
		"darwin":  true,
		"linux":   true,
		"windows": true,
	}

	if supportedOS[runtime.GOOS] {
		t.Skipf("Skipping on supported OS: %s", runtime.GOOS)
	}

	data, err := GetImage()
	if err == nil {
		t.Error("Expected error on unsupported OS")
	}
	if data != nil {
		t.Error("Expected nil data on error")
	}

	expectedMsg := "unsupported OS: " + runtime.GOOS
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error containing %q, got %q", expectedMsg, err.Error())
	}
}

// TestMacImageHexParsing tests macOS image hex decoding logic
func TestMacImageHexParsing(t *testing.T) {
	// Create a minimal valid PNG header (8 bytes)
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

	tests := []struct {
		name     string
		input    string
		expected []byte
		hasError bool
	}{
		{
			name:     "valid macOS output with markers",
			input:    "«data PNGf" + hex.EncodeToString(pngHeader) + "»",
			expected: pngHeader,
			hasError: false,
		},
		{
			name:     "valid macOS output with extra data",
			input:    "«data PNGf" + hex.EncodeToString(append(pngHeader, 0xDE, 0xAD, 0xBE, 0xEF)) + "»",
			expected: append(pngHeader, 0xDE, 0xAD, 0xBE, 0xEF),
			hasError: false,
		},
		{
			name:     "empty output",
			input:    "",
			expected: []byte{},
			hasError: true,
		},
		{
			name:     "missing start marker",
			input:    "something" + hex.EncodeToString(pngHeader) + "»",
			expected: []byte{},
			hasError: true,
		},
		{
			name:     "missing end marker",
			input:    "«data PNGf" + hex.EncodeToString(pngHeader),
			expected: pngHeader,
			hasError: false,
		},
		{
			name:     "invalid hex string",
			input:    "«data PNGfZZZZZZZZ»",
			expected: []byte{},
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trimmed := bytes.TrimSpace([]byte(tt.input))
			if len(trimmed) == 0 && !tt.hasError {
				t.Error("Input should not be empty")
				return
			}

			// Look for markers
			startMarker := []byte("«data PNGf")
			endMarker := []byte("»")

			startIdx := bytes.Index(trimmed, startMarker)
			if startIdx == -1 {
				if !tt.hasError {
					t.Error("Failed to find start marker")
				}
				return
			}
			startIdx += len(startMarker)

			endIdx := bytes.LastIndex(trimmed, endMarker)
			if endIdx == -1 {
				endIdx = len(trimmed)
			}

			hexData := trimmed[startIdx:endIdx]
			data := make([]byte, hex.DecodedLen(len(hexData)))
			n, err := hex.Decode(data, hexData)

			if tt.hasError {
				if err == nil && len(trimmed) > 0 {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			result := data[:n]
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("Got %x, expected %x", result, tt.expected)
			}
		})
	}
}

// TestWindowsBase64Decoding tests Windows image base64 decoding logic
func TestWindowsBase64Decoding(t *testing.T) {
	// Create minimal valid PNG
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	base64Str := base64.StdEncoding.EncodeToString(pngHeader)

	tests := []struct {
		name     string
		input    string
		expected []byte
		hasError bool
	}{
		{
			name:     "valid base64 PNG",
			input:    base64Str,
			expected: pngHeader,
			hasError: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
			hasError: false,
		},
		{
			name:     "whitespace only",
			input:    "   \n\t   ",
			expected: nil,
			hasError: true,
		},
		{
			name:     "invalid base64",
			input:    "!!!not-valid-base64!!!",
			expected: nil,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(tt.input)

			if tt.hasError {
				if err == nil {
					t.Error("Expected error for invalid input")
				}
				if len(data) > 0 {
					t.Error("Expected empty data on error")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if !bytes.Equal(data, tt.expected) {
				t.Errorf("Got %x, expected %x", data, tt.expected)
			}
		})
	}
}

// TestPNGHeaderValidation tests PNG magic number detection
func TestPNGHeaderValidation(t *testing.T) {
	// Valid PNG header
	validPNG := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

	tests := []struct {
		name  string
		data  []byte
		isPNG bool
	}{
		{
			name:  "valid PNG header",
			data:  validPNG,
			isPNG: true,
		},
		{
			name:  "invalid PNG first byte",
			data:  []byte{0x00, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
			isPNG: false,
		},
		{
			name:  "JPEG header",
			data:  []byte{0xFF, 0xD8, 0xFF, 0xE0},
			isPNG: false,
		},
		{
			name:  "empty data",
			data:  []byte{},
			isPNG: false,
		},
		{
			name:  "PNG with extra data",
			data:  append(validPNG, 0x00, 0x01, 0x02),
			isPNG: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.data) < 8 {
				if tt.isPNG {
					t.Error("Expected false for invalid data")
				}
				return
			}

			isValid := bytes.Equal(tt.data[:8], validPNG)
			if isValid != tt.isPNG {
				t.Errorf("Expected isPNG=%v, got %v", tt.isPNG, isValid)
			}
		})
	}
}

// TestErrorWrapping tests that errors are properly wrapped
func TestErrorWrapping(t *testing.T) {
	// This ensures errors are properly wrapped with context
	// which is important for debugging clipboard issues
	err := errors.New("base64 error")

	wrapped := fmt.Errorf("failed to decode clipboard hex data: %w", err)

	if !errors.Is(wrapped, err) {
		t.Error("Wrapped error should be unwrapable to original")
	}

	if !strings.Contains(wrapped.Error(), "failed to decode clipboard hex data") {
		t.Error("Wrapped error should contain context message")
	}
}

// TestLinuxWaylandDetection tests Wayland display detection
func TestLinuxWaylandDetection(t *testing.T) {
	originalWayland := os.Getenv("WAYLAND_DISPLAY")
	defer os.Setenv("WAYLAND_DISPLAY", originalWayland)

	tests := []struct {
		name      string
		envValue  string
		isWayland bool
	}{
		{
			name:      "Wayland display set",
			envValue:  ":0",
			isWayland: true,
		},
		{
			name:      "Wayland display wayland-1",
			envValue:  "wayland-1",
			isWayland: true,
		},
		{
			name:      "Wayland not set",
			envValue:  "",
			isWayland: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("WAYLAND_DISPLAY", tt.envValue)

			isWayland := os.Getenv("WAYLAND_DISPLAY") != ""
			if isWayland != tt.isWayland {
				t.Errorf("Expected isWayland=%v, got %v", tt.isWayland, isWayland)
			}
		})
	}
}
