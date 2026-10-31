package agent

import (
	"path"
	"strings"
)

const sessionIsolateScript = `set -eu
TARGET="$1"
shift
mount --make-rprivate /
STAGING=$(mktemp -d /tmp/.construct-isolate-staging-XXXXXX)
mount --bind "$TARGET" "$STAGING"
mount -t tmpfs -o mode=0755,size=16M tmpfs /workspaces
mkdir -p "$TARGET"
mount --bind "$STAGING" "$TARGET"
umount "$STAGING"
rmdir "$STAGING"
cd "$TARGET"
export HOME=/home/construct
if [ -n "${CONSTRUCT_PATH:-}" ]; then
    export PATH="$CONSTRUCT_PATH"
fi
C_UID=$(id -u construct 2>/dev/null || echo 1000)
C_GID=$(id -g construct 2>/dev/null || echo 1000)
exec setpriv --reuid "$C_UID" --regid "$C_GID" --clear-groups --no-new-privs "$@"`

// wrapIsolatedCommand wraps an agent command in a private mount and PID
// namespace when running inside a guest /workspaces directory. It isolates
// /workspaces so only the active project directory is mounted and visible,
// prevents cross-workspace inspection, preserves the caller's environment,
// drops privileges to construct, and enforces no_new_privs.
func wrapIsolatedCommand(workdir string, args []string) []string {
	if len(args) == 0 {
		return args
	}
	cleanWorkdir := path.Clean(strings.TrimRight(workdir, "/"))
	if cleanWorkdir == "" || cleanWorkdir == "/workspaces" || !strings.HasPrefix(cleanWorkdir, "/workspaces/") {
		return args
	}

	wrapper := make([]string, 0, 15+len(args))
	wrapper = append(wrapper,
		"sudo", "-E", "-n", "unshare",
		"--mount", "--pid", "--fork", "--mount-proc", "--propagation", "private",
		"/bin/sh", "-c", sessionIsolateScript,
		"--", cleanWorkdir,
	)
	return append(wrapper, args...)
}
