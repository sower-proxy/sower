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

const (
	// hostnameTTL bounds how long a resolved hostname is reused before the
	// IP is queried again. Client IPs are stable in practice, so an hour
	// keeps reverse lookups at a few queries per active client per hour.
	hostnameTTL = time.Hour
	// hostnameNegativeTTL bounds how long a failed lookup (empty hostname)
	// is remembered. A resolver blip must not suppress hostnames for the
	// full positive TTL; five minutes keeps retries rare while recovering
	// quickly from transient failures.
	hostnameNegativeTTL = 5 * time.Minute
	// hostnameTimeout bounds each individual reverse lookup. A stalled
	// resolver must never block the traffic view.
	hostnameTimeout = 2 * time.Second
	// hostnameCacheMax bounds the number of cached hostnames. Client counts
	// are small (the stats clients map itself is bounded), so the cache is
	// cleared wholesale when full rather than paying for an eviction
	// structure.
	hostnameCacheMax = 1024
)

type hostnameEntry struct {
	host string
	at   time.Time
}

// hostnameCache memoizes reverse lookups keyed by client IP. It is
// goroutine-safe: lookups run outside the lock, and only the map writes are
// serialized. now is injectable for tests.
type hostnameCache struct {
	mu       sync.Mutex
	byIP     map[string]hostnameEntry
	resolver HostnameResolver
	now      func() time.Time
}

func newHostnameCache(resolver HostnameResolver) *hostnameCache {
	return &hostnameCache{byIP: make(map[string]hostnameEntry), resolver: resolver, now: time.Now}
}

// entryTTL selects the reuse window for a cached entry: failed lookups
// (empty hostname) are retried sooner than successful ones.
func (c *hostnameCache) entryTTL(host string) time.Duration {
	if host == "" {
		return hostnameNegativeTTL
	}
	return hostnameTTL
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
// the injected resolver and reusing cached results within their TTL.
func (c *hostnameCache) attach(clients []ClientStat) {
	if c == nil || len(clients) == 0 {
		return
	}

	now := c.now()
	var unknown []int
	c.mu.Lock()
	for i := range clients {
		if e, ok := c.byIP[clients[i].IP]; ok && now.Sub(e.at) < c.entryTTL(e.host) {
			clients[i].Hostname = e.host
			continue
		}
		unknown = append(unknown, i)
	}
	c.mu.Unlock()

	if len(unknown) == 0 {
		return
	}

	// Deduplicate the batch: the same IP can appear under several entries
	// (per-domain breakdown plus cumulative totals), and one lookup is
	// enough. Resolve unique IPs concurrently so a stalled resolver delays
	// the traffic view by at most one lookup, not by every client in turn.
	// Each write targets distinct slice elements; the cache itself is
	// guarded by its own mutex.
	unique := make(map[string][]int, len(unknown))
	for _, i := range unknown {
		unique[clients[i].IP] = append(unique[clients[i].IP], i)
	}

	var wg sync.WaitGroup
	wg.Add(len(unique))
	for ip, idxs := range unique {
		go func(ip string, idxs []int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), hostnameTimeout)
			host := c.resolver.Hostname(ctx, ip)
			cancel()
			c.store(ip, host)
			for _, i := range idxs {
				clients[i].Hostname = host
			}
		}(ip, idxs)
	}
	wg.Wait()
}

func (c *hostnameCache) store(ip, host string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.byIP) >= hostnameCacheMax {
		c.byIP = make(map[string]hostnameEntry)
	}
	c.byIP[ip] = hostnameEntry{host: host, at: c.now()}
}
