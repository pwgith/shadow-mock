package template

import "fmt"

// fail aborts the template being rendered. raymond recovers panics raised by helpers and
// returns them as the error from Exec, so this is the way a helper reports a problem.
func fail(format string, args ...any) {
	panic(fmt.Errorf(format, args...))
}
