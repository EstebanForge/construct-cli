//go:build !windows

package safefs

import "syscall"

// noFollowFlag fails the open when the final path component is a symlink,
// closing the check-then-open race on the Lstat in ReadFileRegular and
// WriteFile. Undefined on Windows, where the go:build windows fallback
// degrades to the Lstat check alone (WSL runs the linux build).
const noFollowFlag = syscall.O_NOFOLLOW
