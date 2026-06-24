package router

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/maypok86/otter/v2"
)

const (
	accessCacheTTL        = time.Hour
	accessCacheMaxEntries = 1000
)

type accessProbeFunc func(key string) (bool, error)

type accessProbeCache struct {
	cache *otter.Cache[string, bool]
	probe accessProbeFunc
}

func newAccessCache(ttl time.Duration, probe accessProbeFunc) *accessProbeCache {
	return &accessProbeCache{
		cache: otter.Must(&otter.Options[string, bool]{
			MaximumSize:      accessCacheMaxEntries,
			ExpiryCalculator: otter.ExpiryWriting[string, bool](ttl),
		}),
		probe: probe,
	}
}

func (c *accessProbeCache) Get(key string) (bool, error) {
	if c == nil || c.probe == nil {
		return false, nil
	}
	return c.cache.Get(context.Background(), key, otter.LoaderFunc[string, bool](
		func(_ context.Context, key string) (bool, error) {
			return c.probe(key)
		},
	))
}

func (r *Router) isAccess(domain string, port uint16) bool {
	switch port {
	case 80:
	case 443:
	default:
		return false
	}

	ok, _ := r.accessCache.Get(accessCacheKey(domain, port))
	return ok
}

var pingClient = http.Client{
	Timeout: 2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func httpPing(key string) (bool, error) {
	scheme, host, err := accessProbeTarget(key)
	if err != nil {
		return false, err
	}

	resp, err := pingClient.Head(scheme + "://" + host)
	if err != nil {
		slog.Warn("failed to ping", "error", err, "scheme", scheme, "host", host)
		return false, nil
	}
	_ = resp.Body.Close()
	return true, nil
}

func accessCacheKey(domain string, port uint16) string {
	return fmt.Sprintf("%d:%s", port, domain)
}

func accessProbeTarget(key string) (string, string, error) {
	switch {
	case len(key) > 3 && key[:3] == "80:":
		return "http", key[3:], nil
	case len(key) > 4 && key[:4] == "443:":
		return "https", key[4:], nil
	default:
		return "", "", fmt.Errorf("invalid access probe key %q", key)
	}
}
