package router

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/sower-proxy/mem"
)

var accessCache = mem.NewRotateCache(time.Hour, httpPing)

func (r *Router) isAccess(domain string, port uint16) bool {
	switch port {
	case 80:
	case 443:
	default:
		return false
	}

	ok, _ := accessCache.Get(accessCacheKey(domain, port))
	return ok
}

var pingClient = http.Client{
	Timeout: 2 * time.Second,
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
