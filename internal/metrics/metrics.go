// Package metrics exposes the engine's Prometheus metrics (NFR-6.3).
//
// Labels are limited to org, layer, stage, status and method. Session ids, paths, client
// addresses and users are unbounded, so no metric may carry them (CODING_STANDARDS.md §11);
// the methods below take only the permitted values, which keeps that true by construction.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns the registry and every collector.
type Metrics struct {
	reg        *prometheus.Registry
	requests   *prometheus.CounterVec
	matches    *prometheus.CounterVec
	detections *prometheus.CounterVec
	patterns   prometheus.Gauge
	rebuild    prometheus.Histogram
	sessions   prometheus.GaugeFunc
}

// New returns metrics on a private registry. active reports the number of active sessions.
func New(active func() int) *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shadowmock_requests_total", Help: "Mock requests served.",
		}, []string{"org", "method", "status"}),
		matches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shadowmock_matches_total", Help: "Requests served by a stub, by the layer it came from.",
		}, []string{"org", "layer"}),
		detections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "shadowmock_detection_hits_total", Help: "Requests assigned to a shadow mock, by the detection stage that found it.",
		}, []string{"org", "stage"}),
		patterns: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "shadowmock_automaton_patterns", Help: "Patterns in the session detector.",
		}),
		rebuild: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "shadowmock_automaton_rebuild_seconds", Help: "Time to rebuild the session detector.",
			Buckets: prometheus.ExponentialBuckets(0.0001, 4, 8),
		}),
	}
	m.sessions = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "shadowmock_active_sessions", Help: "Active key-based and URL-based shadow mocks.",
	}, func() float64 { return float64(active()) })
	m.reg.MustRegister(m.requests, m.matches, m.detections, m.patterns, m.rebuild, m.sessions)
	return m
}

// Request counts one served mock request. layer and stage are empty when the request matched
// no stub or no shadow mock.
func (m *Metrics) Request(org, method string, status int, layer, stage string) {
	m.requests.WithLabelValues(org, method, strconv.Itoa(status)).Inc()
	if layer != "" {
		m.matches.WithLabelValues(org, layer).Inc()
	}
	if stage != "" {
		m.detections.WithLabelValues(org, stage).Inc()
	}
}

// Rebuilt records a detector rebuild.
func (m *Metrics) Rebuilt(patterns int, took time.Duration) {
	m.patterns.Set(float64(patterns))
	m.rebuild.Observe(took.Seconds())
}

// Handler serves the exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
