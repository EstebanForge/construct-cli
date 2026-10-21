// Package safefs guards host file operations on paths a guest can write
// (the construct home bind): every read refuses symlinks and non-regular
// files, and every write refuses to follow a symlinked destination. A
// prompt-injected agent must not be able to turn a host copy-back or
// config write into an arbitrary-target read or write.
package safefs

import (
	"fmt"
	"io"
	"os"
)

// ReadFileRegular reads path, refusing symlinks and non-regular files and
// capping the size at maxBytes. The staged-file copy-back runs against a
// guest-writable bind: without the refusal a guest-planted symlink makes
// the host read arbitrary bytes (for example a private key) out of the
// bind at teardown.
func ReadFileRegular(path string, maxBytes int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	if fi.Size() > maxBytes {
		return nil, fmt.Errorf("%s: %d bytes exceeds the %d byte copy limit", path, fi.Size(), maxBytes)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only close
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s: grew past the %d byte copy limit mid-read", path, maxBytes)
	}
	return data, nil
}

// WriteFile writes data to path with perm, refusing when path is a
// symlink (dangling or not): os.WriteFile would follow the link and
// create or truncate the attacker-chosen target with host-owned content.
// The Lstat check plus the O_NOFOLLOW open close both the pre-existing
// link case and the swap-between-check-and-open case.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			return fmt.Errorf("refusing to write %s: not a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|noFollowFlag, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
