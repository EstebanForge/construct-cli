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
	"path/filepath"
	"strings"
)

// ReadFileRegular reads path, refusing symlinks (leaf or any directory
// component), non-regular files, and sizes past maxBytes. The staged-file
// copy-back runs against a guest-writable bind: without the refusal a
// guest-planted symlink makes the host read arbitrary bytes (for example
// a private key) out of the bind at teardown.
func ReadFileRegular(path string, maxBytes int64) ([]byte, error) {
	if err := checkDirComponents(path); err != nil {
		return nil, err
	}
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
	// O_NONBLOCK keeps the open from hanging if the file was swapped for a
	// FIFO after the Lstat; the fstat below then refuses the non-regular
	// fd instead of reading (or blocking on) the pipe.
	f, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag|nonBlockFlag, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only close
	openFi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !openFi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s: grew past the %d byte copy limit mid-read", path, maxBytes)
	}
	return data, nil
}

// checkDirComponents Lstats every directory component of path (the leaf
// itself is covered by the callers' leaf checks): each existing component
// must be a real directory, not a symlink. O_NOFOLLOW guards only the
// final component, and a guest-planted symlinked directory under the bind
// would redirect every later host open persistently — no race required.
func checkDirComponents(path string) error {
	dir := filepath.Dir(path)
	if dir == "." {
		return nil
	}
	prefix := ""
	if filepath.IsAbs(dir) {
		prefix = string(os.PathSeparator)
	}
	for _, c := range strings.Split(dir, string(os.PathSeparator)) {
		if c == "" || c == "." {
			continue
		}
		prefix = filepath.Join(prefix, c)
		fi, err := os.Lstat(prefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // the caller's open fails on the missing dir anyway
			}
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("%s: %s is not a real directory", path, prefix)
		}
	}
	return nil
}

// WriteFile writes data to path with perm, refusing when path (or any
// directory component) is a symlink: os.WriteFile would follow the link
// and create or truncate the attacker-chosen target with host-owned
// content. The write itself lands via a temp file in the target directory
// plus rename, which never follows a leaf swapped in after the check.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if err := checkDirComponents(path); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			return fmt.Errorf("refusing to write %s: not a regular file", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, perm)
	}
	if werr != nil {
		os.Remove(tmp) //nolint:errcheck // best-effort cleanup on failure
		return werr
	}
	return os.Rename(tmp, path)
}
