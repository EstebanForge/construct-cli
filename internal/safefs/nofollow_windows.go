//go:build windows

package safefs

// noFollowFlag: no direct O_NOFOLLOW equivalent; the Lstat check in
// ReadFileRegular and WriteFile still refuses pre-existing symlinks.
const noFollowFlag = 0
