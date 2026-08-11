package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	sort, source, client := parseTrafficQuery(r)
	snap := s.opts.Stats.Snapshot(sort, source, client)
	s.attachHostnames(snap.Clients)
	writeJSON(w, http.StatusOK, snap)
}

// handleTotals serves the from-start cumulative traffic view. It ignores
// staleness and query filters: totals are the whole picture, not a window.
func (s *Server) handleTotals(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.totalsWithHostnames())
}

// totalsWithHostnames decorates the cumulative totals with resolved client
// hostnames, shared by the HTTP endpoint and the SSE stream.
func (s *Server) totalsWithHostnames() Totals {
	totals := s.opts.Stats.Totals()
	s.attachHostnames(totals.Clients)
	return totals
}

// parseTrafficQuery extracts the domain sort, source, and client filters
// shared by /api/traffic and the SSE stream.
func parseTrafficQuery(r *http.Request) (sort DomainSort, source Source, client string) {
	sort = DomainSort(r.URL.Query().Get("sort"))
	if !sort.valid() {
		sort = DomainSortBytes
	}
	source = Source(r.URL.Query().Get("source"))
	if !source.valid() {
		source = SourceAll
	}
	client = r.URL.Query().Get("client")
	return
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Stats.History())
}

// streamTrafficSnapshot caches the unfiltered live view for one stream tick.
// Filtered views remain immediate to avoid an unbounded per-client cache.
func (s *Server) streamTrafficSnapshot(sort DomainSort, source Source, client string) TrafficSnapshot {
	var snap TrafficSnapshot
	if sort != DomainSortBytes || source != SourceAll || client != "" {
		snap = s.opts.Stats.Snapshot(sort, source, client)
	} else {
		now := time.Now()
		s.trafficMu.Lock()
		defer s.trafficMu.Unlock()
		if !s.traffic.at.IsZero() && now.Sub(s.traffic.at) < trafficCacheTTL {
			return s.traffic.snapshot
		}
		s.traffic.snapshot = s.opts.Stats.Snapshot(sort, source, client)
		s.traffic.at = now
		snap = s.traffic.snapshot
	}
	s.attachHostnames(snap.Clients)
	return snap
}

// sseWriteTimeout bounds each individual SSE write. A client that vanished
// without closing the TCP connection (asleep laptop, dropped WiFi) would
// otherwise block the handler forever once the kernel send buffer fills;
// the deadline turns that into a bounded, detectable failure.
const sseWriteTimeout = 30 * time.Second

// handleStream pushes status, traffic snapshots, and history to the console.
// The connection carries the same sort/source/client filters as /api/traffic
// and revalidates the session on each tick. When a sliding renewal is due it
// asks the browser to refresh the cookie through /api/session, then closes so
// EventSource reconnects with the renewed cookie. Expiry sends an auth event.
// The initial payload is sent immediately so the page renders without waiting
// for a tick. Every write carries a deadline (see sseWriteTimeout); a failed
// write means the client is gone and the handler exits instead of leaking.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	rc := http.NewResponseController(w)
	sort, source, client := parseTrafficQuery(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprintln(w, "retry: 5000")
	fmt.Fprintln(w)
	// A writer that cannot flush (buffered proxy, unsupported transport)
	// cannot carry the stream; the error-returning form also surfaces dead
	// connections here.
	if err := rc.Flush(); err != nil {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// send writes one event and reports whether the connection is still
	// usable; the caller tears the stream down on the first failure. A
	// non-writable transport (HTTP/2) degrades to unbounded writes instead
	// of failing the stream.
	send := func(event string, v any) bool {
		if err := rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil {
			slog.Debug("set sse write deadline", "error", err)
		}
		data, err := json.Marshal(v)
		if err != nil {
			slog.Debug("marshal sse event", "error", err)
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	if !send("status", s.statusPayload()) ||
		!send("traffic", s.streamTrafficSnapshot(sort, source, client)) ||
		!send("history", s.opts.Stats.History()) ||
		!send("totals", s.totalsWithHostnames()) {
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	historyTicker := time.NewTicker(10 * time.Second)
	defer historyTicker.Stop()
	totalsTicker := time.NewTicker(30 * time.Second)
	defer totalsTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, valid, renewed := s.validSession(r)
			if !valid {
				send("auth", map[string]any{"status": http.StatusUnauthorized})
				return
			}
			if renewed {
				send("renew", map[string]any{})
				return
			}
			if !send("status", s.statusPayload()) || !send("traffic", s.streamTrafficSnapshot(sort, source, client)) {
				return
			}
		case <-historyTicker.C:
			if !send("history", s.opts.Stats.History()) {
				return
			}
		case <-totalsTicker.C:
			if !send("totals", s.totalsWithHostnames()) {
				return
			}
		}
	}
}
