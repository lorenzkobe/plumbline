package core

import (
	"os"
	"testing"

	"github.com/lorenzkobe/plumbline/internal/testshard"
)

// Each test runs in its own process: the harness's t.Setenv and package
// clocks rule out t.Parallel.
func TestMain(m *testing.M) { os.Exit(testshard.Main(m)) }
