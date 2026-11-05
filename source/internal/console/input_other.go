//go:build !windows

package console

// rawInput is a no-op outside Windows: keys arrive after Enter.
func rawInput() (restore func(), ok bool) { return func() {}, false }
