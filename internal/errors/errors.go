// Package errors defines custom error types and categories for Construct CLI.
package errors

import (
	"fmt"
	"strings"
)

// ErrorCategory represents the type of error that occurred.
type ErrorCategory string

const (
	// ErrorCategoryRuntime represents errors occurring during container runtime operations.
	ErrorCategoryRuntime ErrorCategory = "RUNTIME"
	// ErrorCategoryConfig represents errors related to configuration loading or validation.
	ErrorCategoryConfig ErrorCategory = "CONFIG"
	// ErrorCategoryPermission represents errors caused by insufficient permissions.
	ErrorCategoryPermission ErrorCategory = "PERMISSION"
	// ErrorCategoryNetwork represents errors related to network management or isolation.
	ErrorCategoryNetwork ErrorCategory = "NETWORK"
	// ErrorCategoryFile represents errors related to file system operations.
	ErrorCategoryFile ErrorCategory = "FILE"
	// ErrorCategoryContainer represents errors occurring inside or while interacting with a container.
	ErrorCategoryContainer ErrorCategory = "CONTAINER"
)

// ConstructError wraps errors with context for better diagnostics
type ConstructError struct {
	Category   ErrorCategory
	Operation  string // What was being attempted
	Path       string // File path if relevant
	Command    string // Command being executed if relevant
	Runtime    string // Container runtime if relevant
	Suggestion string // Actionable recovery suggestion
	Err        error  // Original error
}

func (e *ConstructError) Error() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[%s] %s failed", e.Category, e.Operation))
	if e.Path != "" {
		b.WriteString(fmt.Sprintf("\n  Path: %s", e.Path))
	}
	if e.Command != "" {
		b.WriteString(fmt.Sprintf("\n  Command: %s", e.Command))
	}
	if e.Runtime != "" {
		b.WriteString(fmt.Sprintf("\n  Runtime: %s", e.Runtime))
	}
	if e.Err != nil {
		b.WriteString(fmt.Sprintf("\n  Cause: %v", e.Err))
	}
	if e.Suggestion != "" {
		b.WriteString(fmt.Sprintf("\n  → %s", e.Suggestion))
	}
	return b.String()
}
