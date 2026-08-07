// Package metrics owns the OpenTelemetry meter and instruments for sower. It
// is the standardized export surface: the Prometheus /metrics endpoint today,
// and an OTLP exporter can be added later without touching call sites. The
// admin console reads its own display counters (internal/admin.Stats), not
// these instruments, so the UI never depends on exporter internals.
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	rtmetrics "runtime/metrics"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Metrics owns the OTel meter, instruments, and the Prometheus export handler.
type Metrics struct {
	provider *sdkmetric.MeterProvider
	handler  http.Handler
	meter    metric.Meter

	bytes       metric.Int64Counter // attr: direction=up|down
	dnsQueries  metric.Int64Counter
	conns       metric.Int64Counter // attr: protocol=http|https|socks5
	activeConns metric.Int64UpDownCounter
	ruleHits    metric.Int64Counter // attr: route=block|direct|proxy
	connDur     metric.Int64Histogram

	start time.Time
}

// New creates the meter provider and all instruments. Instrument definitions
// are programmer errors and panic rather than fail silently; only provider
// creation can return an error.
func New() (*Metrics, error) {
	// A fresh registry per instance: the exporter registers itself as a
	// prometheus.Collector, and the global default registerer would reject a
	// second instance (tests create several).
	reg := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(reg), otelprom.WithoutScopeInfo())
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	m := &Metrics{
		provider: provider,
		handler:  promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		meter:    provider.Meter("sower"),
		start:    time.Now(),
	}

	m.bytes = mustCounter(m.meter, "sower.bytes",
		metric.WithDescription("Proxied payload bytes, direction relative to the client"),
		metric.WithUnit("By"))
	m.dnsQueries = mustCounter(m.meter, "sower.dns_queries",
		metric.WithDescription("DNS queries answered by the proxy"))
	m.conns = mustCounter(m.meter, "sower.connections",
		metric.WithDescription("Proxy connections accepted"))
	m.activeConns = mustUpDownCounter(m.meter, "sower.connections_active",
		metric.WithDescription("Proxy connections currently open"))
	m.ruleHits = mustCounter(m.meter, "sower.rule_hits",
		metric.WithDescription("Connection routing decisions by rule category"))
	m.connDur = mustHistogram(m.meter, "sower.connection_duration",
		metric.WithDescription("Proxy connection lifetime"),
		metric.WithUnit("ms"))

	goroutines := mustObservableGauge(m.meter, "sower.process_goroutines",
		metric.WithDescription("Goroutines in the sower process"))
	heap := mustObservableGauge(m.meter, "sower.process_heap_alloc",
		metric.WithDescription("Heap bytes allocated by the sower process"),
		metric.WithUnit("By"))
	uptime := mustObservableGauge(m.meter, "sower.uptime_seconds",
		metric.WithDescription("Seconds since the sower process started"),
		metric.WithUnit("s"))
	if _, err := m.meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
		o.ObserveInt64(heap, int64(heapAlloc()))
		o.ObserveInt64(uptime, int64(time.Since(m.start).Seconds()))
		return nil
	}, goroutines, heap, uptime); err != nil {
		return nil, fmt.Errorf("register process gauges: %w", err)
	}

	return m, nil
}

// Precomputed AddOptions avoid an attribute-slice allocation on every
// RecordBytes call, which runs on the per-I/O relay hot path.
var (
	bytesAttrUp   = metric.WithAttributes(attribute.String("direction", "up"))
	bytesAttrDown = metric.WithAttributes(attribute.String("direction", "down"))
)

// RecordBytes counts proxied payload bytes. up is relative to the client:
// reads from the client are uploads, writes to the client downloads.
func (m *Metrics) RecordBytes(up bool, n int) {
	if up {
		m.bytes.Add(context.Background(), int64(n), bytesAttrUp)
	} else {
		m.bytes.Add(context.Background(), int64(n), bytesAttrDown)
	}
}

// RecordDNS counts one DNS query.
func (m *Metrics) RecordDNS() {
	m.dnsQueries.Add(context.Background(), 1)
}

// ConnOpened counts a new connection and raises the active gauge.
func (m *Metrics) ConnOpened(protocol string) {
	attr := attribute.String("protocol", protocol)
	m.conns.Add(context.Background(), 1, metric.WithAttributes(attr))
	m.activeConns.Add(context.Background(), 1, metric.WithAttributes(attr))
}

// ConnClosed lowers the active gauge for one connection.
func (m *Metrics) ConnClosed(protocol string) {
	m.activeConns.Add(context.Background(), -1,
		metric.WithAttributes(attribute.String("protocol", protocol)))
}

// RecordRoute counts one connection routing decision.
func (m *Metrics) RecordRoute(route string) {
	m.ruleHits.Add(context.Background(), 1,
		metric.WithAttributes(attribute.String("route", route)))
}

// RecordConnDuration records a connection lifetime.
func (m *Metrics) RecordConnDuration(d time.Duration) {
	m.connDur.Record(context.Background(), d.Milliseconds())
}

// ServeHTTP serves the Prometheus exposition format.
func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.handler.ServeHTTP(w, r)
}

// Shutdown flushes and stops the meter provider.
func (m *Metrics) Shutdown(ctx context.Context) error {
	return m.provider.Shutdown(ctx)
}

// heapAlloc reads the live heap size without stopping the world.
func heapAlloc() uint64 {
	var samples = []rtmetrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	rtmetrics.Read(samples)
	return samples[0].Value.Uint64()
}

func mustCounter(meter metric.Meter, name string, opts ...metric.Int64CounterOption) metric.Int64Counter {
	c, err := meter.Int64Counter(name, opts...)
	if err != nil {
		panic(fmt.Sprintf("metrics: invalid counter %q: %v", name, err))
	}
	return c
}

func mustUpDownCounter(meter metric.Meter, name string, opts ...metric.Int64UpDownCounterOption) metric.Int64UpDownCounter {
	c, err := meter.Int64UpDownCounter(name, opts...)
	if err != nil {
		panic(fmt.Sprintf("metrics: invalid updown counter %q: %v", name, err))
	}
	return c
}

func mustHistogram(meter metric.Meter, name string, opts ...metric.Int64HistogramOption) metric.Int64Histogram {
	h, err := meter.Int64Histogram(name, opts...)
	if err != nil {
		panic(fmt.Sprintf("metrics: invalid histogram %q: %v", name, err))
	}
	return h
}

func mustObservableGauge(meter metric.Meter, name string, opts ...metric.Int64ObservableGaugeOption) metric.Int64ObservableGauge {
	g, err := meter.Int64ObservableGauge(name, opts...)
	if err != nil {
		panic(fmt.Sprintf("metrics: invalid gauge %q: %v", name, err))
	}
	return g
}
