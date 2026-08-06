package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExporterServesInstruments(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatalf("new metrics: %v", err)
	}
	defer m.Shutdown(t.Context())

	m.RecordBytes(true, 5)
	m.RecordBytes(false, 7)
	m.RecordDNS()
	m.ConnOpened("http")
	m.ConnClosed("http")
	m.RecordRoute("proxy")
	m.RecordConnDuration(3 * time.Millisecond)

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`sower_bytes_total{direction="up"} 5`,
		`sower_bytes_total{direction="down"} 7`,
		"sower_dns_queries_total 1",
		`sower_connections_total{protocol="http"} 1`,
		`sower_connections_active{protocol="http"} 0`,
		`sower_rule_hits_total{route="proxy"} 1`,
		"sower_uptime_seconds ",
		"sower_process_goroutines ",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in metrics, got:\n%s", want, body)
		}
	}
}

func TestMultipleInstancesDoNotConflict(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatalf("new metrics a: %v", err)
	}
	defer a.Shutdown(t.Context())
	b, err := New()
	if err != nil {
		t.Fatalf("new metrics b: %v", err)
	}
	defer b.Shutdown(t.Context())

	a.RecordDNS()
	b.RecordDNS()
	b.RecordDNS()

	ra := httptest.NewRecorder()
	a.ServeHTTP(ra, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(ra.Body.String(), "sower_dns_queries_total 1") {
		t.Fatalf("expected instance a to report 1 query, got:\n%s", ra.Body.String())
	}
	rb := httptest.NewRecorder()
	b.ServeHTTP(rb, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rb.Body.String(), "sower_dns_queries_total 2") {
		t.Fatalf("expected instance b to report 2 queries, got:\n%s", rb.Body.String())
	}
}
