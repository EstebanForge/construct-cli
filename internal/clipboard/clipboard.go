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
)

var ErrNoImage = errors.New("no image in clipboard")
var ErrNoText = errors.New("no text in clipboard")

// GetText retrieves text data from the host clipboard
func GetText() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return getMacText()
	case "linux":
		return getLinuxText()
	case "windows":
		return getWindowsText()
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// GetImage retrieves PNG data from the host clipboard using OS-specific tools
func GetImage() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return getMacImage()
	case "linux":
		return getLinuxImage()
	case "windows":
		return getWindowsImage()
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func getMacImage() ([]byte, error) {
	// Request clipboard as PNG data. osascript will return it as a hex string
	// in the format: «data PNGf89504E47...»
	script := "get the clipboard as «class PNGf»"
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If it fails, maybe no image
		return nil, ErrNoImage
	}

	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return nil, ErrNoImage
	}

	// Look for the hex start. PNG magic is 89504E47.
	// The output is usually «data PNGf89504E47...»
	startMarker := []byte("«data PNGf")
	endMarker := []byte("»")
	
	startIdx := bytes.Index(trimmed, startMarker)
	if startIdx == -1 {
		return nil, ErrNoImage
	}
	startIdx += len(startMarker)
	
	endIdx := bytes.LastIndex(trimmed, endMarker)
	if endIdx == -1 {
		endIdx = len(trimmed)
	}

	hexData := trimmed[startIdx:endIdx]
	
	// Decode hex
	data := make([]byte, hex.DecodedLen(len(hexData)))
	n, err := hex.Decode(data, hexData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode clipboard hex data: %w", err)
	}

	return data[:n], nil
}

func getLinuxImage() ([]byte, error) {
	// Try wl-paste (Wayland) first, then xclip (X11)
	
	// Check if we are in Wayland
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		cmd := exec.Command("wl-paste", "-t", "image/png")
		data, err := cmd.Output()
		if err == nil && len(data) > 0 {
			return data, nil
		}
		// Fallthrough to xclip if wl-paste fails or not present
	}

	// Try xclip
	cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	data, err := cmd.Output()
	if err != nil {
		// Verify if xclip is installed
		if _, lookErr := exec.LookPath("xclip"); lookErr != nil {
			return nil, fmt.Errorf("xclip or wl-paste not found on host")
		}
		// xclip returns error if no target found (no image)
		return nil, ErrNoImage
	}

	if len(data) == 0 {
		return nil, ErrNoImage
	}

	return data, nil
}

func getMacText() ([]byte, error) {
	cmd := exec.Command("pbpaste")
	return cmd.Output()
}

func getLinuxText() ([]byte, error) {
	// Try wl-paste
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		cmd := exec.Command("wl-paste", "--no-newline")
		data, err := cmd.Output()
		if err == nil {
			return data, nil
		}
	}
	// Try xclip
	cmd := exec.Command("xclip", "-selection", "clipboard", "-o")
	return cmd.Output()
}

func getWindowsText() ([]byte, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw")
	return cmd.Output()
}

func getWindowsImage() ([]byte, error) {
	// Use PowerShell to get clipboard image
	// We use a script to output base64 then decode
	
	psScript := `
		Add-Type -AssemblyName System.Windows.Forms
		$img = [System.Windows.Forms.Clipboard]::GetImage()
		if ($img -eq $null) { exit 1 }
		$ms = New-Object System.IO.MemoryStream
		$img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
		$base64 = [Convert]::ToBase64String($ms.ToArray())
		Write-Output $base64
	`
	
	// Note: System.Windows.Forms requires STA mode (-Sta)
	cmd := exec.Command("powershell", "-NoProfile", "-Sta", "-Command", psScript)
	output, err := cmd.Output()
	if err != nil {
		return nil, ErrNoImage // Assume no image or failure
	}

	base64Str := string(bytes.TrimSpace(output))
	if base64Str == "" {
		return nil, ErrNoImage
	}

	data, err := base64.StdEncoding.DecodeString(base64Str)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 image data: %w", err)
	}
	
	return data, nil
}