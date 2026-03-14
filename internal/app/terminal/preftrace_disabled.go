//go:build !perftrace

package terminalOut

var perfNoop = func() {}

func PerfStart(_ string) func() {
	return perfNoop
}
