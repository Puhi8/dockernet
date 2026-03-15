//go:build !perftrace && !debug

package terminalOut

func Log(_ string) {}

func Logf(_ string, _ ...any) {}
