//go:build unix

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// dumpHeapOnSignal writes a heap profile per SIGUSR1 into the cache dir, so a
// heap that has grown can be attributed to an allocation site without adding
// a network endpoint ('systemctl kill -s USR1 sowerd'; read it with
// 'go tool pprof -http :0 <file>'). Sending the signal needs root or the
// service's own uid, so the trigger stays local.
func dumpHeapOnSignal(ctx context.Context, dir string) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	defer signal.Stop(signals)

	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			path, err := dumpHeapProfile(dir)
			if err != nil {
				slog.Warn("write heap profile", "error", err)
				continue
			}
			slog.Info("heap profile written", "path", path)
		}
	}
}
