//go:build windows

package safefs

// noFollowFlag: no direct O_NOFOLLOW equivalent; the Lstat check in
// ReadFileRegular and WriteFile still refuses pre-existing symlinks.
const noFollowFlag = 0

// nonBlockFlag: no O_NONBLOCK equivalent needed — the Lstat check plus the
// post-open fstat cover the FIFO case on Windows builds.
const nonBlockFlag = 0
