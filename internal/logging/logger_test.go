// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package logging

import (
	"testing"
)

func TestDebugLogger(t *testing.T) {
	func() {
		_ = recover()
		NewLogger("DEBUG")
	}()
}

func TestInvalidLevel(t *testing.T) {
	func() {
		_ = recover()
		NewLogger("invalid")
	}()
}
