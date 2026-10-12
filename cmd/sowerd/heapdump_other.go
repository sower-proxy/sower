//go:build !unix

package main

import "context"

// dumpHeapOnSignal is a no-op where SIGUSR1 is not defined; the heap profile
// itself (dumpHeapProfile) is portable.
func dumpHeapOnSignal(context.Context, string) {}
