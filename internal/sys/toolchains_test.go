package sys

import (
	"testing"
)

func TestHandleToolchainsCommand(t *testing.T) {
	t.Helper()
	// Should not panic on list or default
	HandleToolchainsCommand([]string{"list"})
	HandleToolchainsCommand(nil)
}
