//go:build !windows

package app

// PortOwnerHint is only implemented on Windows; elsewhere the start error
// already carries the address and the OS error.
func PortOwnerHint(addr string) string { return "" }
