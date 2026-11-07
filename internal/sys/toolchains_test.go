package sys

import (
	"testing"
)

func TestHandleToolchainsCommand(t *testing.T) {
	t.Helper()
	// Should not panic on list or default
	HandleToolchainsCommand([]string{"list"})
	HandleToolchainsCommand(nil)
	HandleToolchainsCommand([]string{"help"})
	HandleToolchainsCommand([]string{"fetch"})
}
