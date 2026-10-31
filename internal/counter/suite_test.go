package counter_test

import (
	"testing"

	"github.com/pwgith/shadow-mock/internal/counter"
	"github.com/pwgith/shadow-mock/internal/countertest"
)

func TestMemoryPassesTheSuite(t *testing.T) {
	t.Parallel()
	countertest.Run(t, func(*testing.T) counter.Counters { return counter.NewMemory() })
}
