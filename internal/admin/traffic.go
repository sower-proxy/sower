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
	writeJSON(w, http.StatusOK, s.opts.Stats.Snapshot(sort, source, client))
}

// handleTotals serves the from-start cumulative traffic view. It ignores
// staleness and query filters: totals are the whole picture, not a window.
func (s *Server) handleTotals(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Stats.Totals())
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
	if sort != DomainSortBytes || source != SourceAll || client != "" {
		return s.opts.Stats.Snapshot(sort, source, client)
	}

	now := time.Now()
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()
	if !s.traffic.at.IsZero() && now.Sub(s.traffic.at) < trafficCacheTTL {
		return s.traffic.snapshot
	}
	s.traffic.snapshot = s.opts.Stats.Snapshot(sort, source, client)
	s.traffic.at = now
	return s.traffic.snapshot
}

// handleStream pushes status, traffic snapshots, and history to the console.
// The connection carries the same sort/source/client filters as /api/traffic
// and revalidates the session on each tick, closing with an auth event when
// it lapses. The initial payload is sent immediately so the page renders
// without waiting for a tick.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	sort, source, client := parseTrafficQuery(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprintln(w, "retry: 5000")
	fmt.Fprintln(w)
	flusher.Flush()

	send := func(event string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			slog.Debug("marshal sse event", "error", err)
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	send("status", s.statusPayload())
	send("traffic", s.streamTrafficSnapshot(sort, source, client))
	send("history", s.opts.Stats.History())
	send("totals", s.opts.Stats.Totals())

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
			if !s.validSession(r) {
				send("auth", map[string]any{"status": http.StatusUnauthorized})
				return
			}
			send("status", s.statusPayload())
			send("traffic", s.streamTrafficSnapshot(sort, source, client))
		case <-historyTicker.C:
			send("history", s.opts.Stats.History())
		case <-totalsTicker.C:
			send("totals", s.opts.Stats.Totals())
		}
	}
}
