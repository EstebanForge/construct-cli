package ui

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Log levels
const (
	LogLevelError   = 0
	LogLevelWarning = 1
	LogLevelInfo    = 2
	LogLevelDebug   = 3
)

// CurrentLogLevel is the global logging level
var CurrentLogLevel = LogLevelWarning

func SetLogLevel(level int) {
	CurrentLogLevel = level
}

func LogError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
}

func LogWarning(msg string, args ...interface{}) {
	if CurrentLogLevel >= LogLevelWarning {
		fmt.Fprintf(os.Stderr, "Warning: "+msg+"\n", args...)
	}
}

func LogInfo(msg string, args ...interface{}) {
	if CurrentLogLevel >= LogLevelInfo {
		fmt.Printf("Info: "+msg+"\n", args...)
	}
}

func LogDebug(msg string, args ...interface{}) {
	if CurrentLogLevel >= LogLevelDebug {
		fmt.Printf("Debug: "+msg+"\n", args...)
	}
}

// ANSI Colors
const (
	ColorPink   = "\033[38;5;212m"
	ColorRed    = "\033[38;5;196m"
	ColorOrange = "\033[38;5;214m"
	ColorCyan   = "\033[38;5;86m"
	ColorGrey   = "\033[38;5;242m"
	ColorReset  = "\033[0m"
	ColorBold   = "\033[1m"
)

// GumAvailable checks if Gum is available
func GumAvailable() bool {
	_, err := exec.LookPath("gum")
	return err == nil
}

// GumSuccess prints a success message using ANSI colors
func GumSuccess(msg string) {
	fmt.Printf("%s✓ %s%s\n", ColorPink, msg, ColorReset)
}

// GumError prints an error message using ANSI colors
func GumError(msg string) {
	fmt.Fprintf(os.Stderr, "%s✗ %s%s\n", ColorRed, msg, ColorReset)
}

// GumWarning prints a warning message using ANSI colors
func GumWarning(msg string) {
	fmt.Printf("%s⚠️  %s%s\n", ColorOrange, msg, ColorReset)
}

// GumInfo prints an info message using ANSI colors
func GumInfo(msg string) {
	fmt.Printf("%sℹ️  %s%s\n", ColorCyan, msg, ColorReset)
}

// GetGumCommand returns an exec.Cmd for gum with environment variables set to suppress terminal queries
func GetGumCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("gum", args...)
	cmd.Env = append(os.Environ(), "LIPGLOSS_HAS_DARK_BACKGROUND=true")
	return cmd
}

// GumSpinner runs a function with a spinner if Gum is available
func GumSpinner(title string, fn func() []string) []string {
	if !GumAvailable() {
		return fn()
	}

	// Run function in background
	resultChan := make(chan []string)
	go func() {
		resultChan <- fn()
	}()

	// Show spinner while waiting
	spinner := GetGumCommand("spin", "--spinner", "dot", "--title", title, "--", "sleep", "10")
	spinner.Stdout = os.Stdout
	spinner.Stderr = os.Stderr
	spinner.Start()

	result := <-resultChan
	if spinner.Process != nil {
		spinner.Process.Kill()
	}

	return result
}

// GumConfirm prompts for confirmation using Gum if available
func GumConfirm(prompt string) bool {
	if !GumAvailable() {
		fmt.Printf("%s [y/N]: ", prompt)
		var response string
		fmt.Scanln(&response)
		return response == "y" || response == "Y"
	}

	cmd := GetGumCommand("confirm", prompt)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return err == nil
}

// RunCommandWithSpinner runs a command with a native spinner and interactive log peeking
func RunCommandWithSpinner(cmd *exec.Cmd, title string, logFile *os.File) error {
	// Redirect output to log file
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Channel to signal command completion
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// Channel for user input (Enter key)
	input := make(chan bool)
	go func() {
		// Read one byte at a time. In canonical mode (default), this returns after Enter.
		var b [1]byte
		for {
			_, err := os.Stdin.Read(b[:])
			if err != nil {
				return
			}
			input <- true
		}
	}()

	// Spinner settings
	spinnerChars := []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
	i := 0
	ticker := time.NewTicker(80 * time.Millisecond) // Smooth animation
	defer ticker.Stop()

	// If not a TTY or gum not available, simpler output
	// Simplified check
	isTTY := os.Getenv("TERM") != ""
	if !isTTY {
		fmt.Println(title)
		return <-done
	}

	for {
		select {
		case err := <-done:
			// Clear the line and move to next
			fmt.Printf("\r\033[K")

			// If failed, print tail of logs
			if err != nil && logFile != nil {
				if GumAvailable() {
					GumError("Command failed. Last 20 lines of log:")
					tailCmd := exec.Command("tail", "-n", "20", logFile.Name())
					output, _ := tailCmd.CombinedOutput()
					styleCmd := GetGumCommand("style", "--foreground", "242", "--border", "normal", "--padding", "0 1", string(output))
					styleCmd.Stdout = os.Stdout
					styleCmd.Run()
				} else {
					fmt.Printf("Command failed. Last 20 lines of log (%s):\n", logFile.Name())
					tailCmd := exec.Command("tail", "-n", "20", logFile.Name())
					tailCmd.Stdout = os.Stdout
					tailCmd.Run()
				}
			}
			return err

		case <-input:
			// User pressed Enter: Peek logs
			fmt.Printf("\r\033[K") // Clear line
			fmt.Printf("%s--- Log Snapshot (Last 10 lines) ---%s\n", ColorGrey, ColorReset)

			if logFile != nil {
				tailCmd := exec.Command("tail", "-n", "10", logFile.Name())
				output, _ := tailCmd.CombinedOutput()
				fmt.Print(string(output))
			} else {
				fmt.Println("(No log file)")
			}
			fmt.Printf("%s------------------------------------%s\n", ColorGrey, ColorReset)
			// Loop continues, spinner will redraw on next tick

		case <-ticker.C:
			// Draw spinner
			// \r = return to start, \033[K = clear line
			spinner := spinnerChars[i%len(spinnerChars)]
			fmt.Printf("\r\033[K%s%s%s %s %s(Press Enter to peek logs)%s",
				ColorPink, spinner, ColorReset,
				title,
				ColorGrey, ColorReset)
			i++
		}
	}
}
