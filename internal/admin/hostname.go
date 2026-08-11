package admin

import (
	"context"
	"sync"
	"time"
)

// HostnameResolver resolves a client IP to a display hostname for the traffic
// console. Implementations are injected via Options; when nil the console
// shows raw IPs and no reverse lookups are issued.
type HostnameResolver interface {
	Hostname(ctx context.Context, ip string) string
}

// hostnameTTL bounds how long a resolved hostname is reused before the IP is
// queried again. Client IPs are stable in practice, so an hour keeps reverse
// lookups at a few queries per active client per hour.
const hostnameTTL = time.Hour

// hostnameTimeout bounds each individual reverse lookup. A stalled resolver
// must never block the traffic view.
const hostnameTimeout = 2 * time.Second

// hostnameCacheMax bounds the number of cached hostnames. Client counts are
// small (the stats clients map itself is bounded), so the cache is cleared
// wholesale when full rather than paying for an eviction structure.
const hostnameCacheMax = 1024

type hostnameEntry struct {
	host string
	at   time.Time
}

// hostnameCache memoizes reverse lookups keyed by client IP. It is
// goroutine-safe: lookups run outside the lock, and only the map writes are
// serialized.
type hostnameCache struct {
	mu       sync.Mutex
	byIP     map[string]hostnameEntry
	resolver HostnameResolver
}

func newHostnameCache(resolver HostnameResolver) *hostnameCache {
	return &hostnameCache{byIP: make(map[string]hostnameEntry), resolver: resolver}
}

// attachHostnames decorates client stats with resolved hostnames. It is a
// no-op when reverse lookups are not configured.
func (s *Server) attachHostnames(clients []ClientStat) {
	if s.hostnames == nil {
		return
	}
	s.hostnames.attach(clients)
}

// attach fills Hostname on each client stat, resolving unknown IPs through
// the injected resolver and reusing cached results within hostnameTTL.
func (c *hostnameCache) attach(clients []ClientStat) {
	if c == nil || len(clients) == 0 {
		return
	}

	now := time.Now()
	var unknown []int
	c.mu.Lock()
	for i := range clients {
		if e, ok := c.byIP[clients[i].IP]; ok && now.Sub(e.at) < hostnameTTL {
			clients[i].Hostname = e.host
			continue
		}
		unknown = append(unknown, i)
	}
	c.mu.Unlock()

	if len(unknown) == 0 {
		return
	}

	// Resolve unknown IPs concurrently so a stalled resolver delays the
	// traffic view by at most one lookup, not by every client in turn.
	// Each write targets a distinct slice element; the cache itself is
	// guarded by its own mutex.
	var wg sync.WaitGroup
	wg.Add(len(unknown))
	for _, i := range unknown {
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), hostnameTimeout)
			host := c.resolver.Hostname(ctx, clients[i].IP)
			cancel()
			c.store(clients[i].IP, host, now)
			clients[i].Hostname = host
		}(i)
	}
	wg.Wait()
}

func (c *hostnameCache) store(ip, host string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.byIP) >= hostnameCacheMax {
		c.byIP = make(map[string]hostnameEntry)
	}
	c.byIP[ip] = hostnameEntry{host: host, at: now}
}
