package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// SyncMetrics contains all metrics related to the sync process
var SyncMetrics = struct {
	SyncDuration prometheus.Histogram
	SyncErrors   *prometheus.CounterVec
	SyncSuccess  prometheus.Counter
	BooksSynced  prometheus.Counter
	BooksSkipped prometheus.Counter
	ProgressUpdates prometheus.Counter
	RateLimitHits prometheus.Counter
	APIRequests   *prometheus.CounterVec
	APILatency   *prometheus.HistogramVec
	CircuitBreakerTripped prometheus.Counter
	Retries      *prometheus.CounterVec
}{
	SyncDuration: promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "abs_hardcover_sync_duration_seconds",
		Help: "Duration of sync operations in seconds",
		Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0},
	}),
	SyncErrors: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "abs_hardcover_sync_errors_total",
		Help: "Total number of sync errors by type",
	}, []string{"error_type"}),
	SyncSuccess: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_sync_success_total",
		Help: "Total number of successful sync operations",
	}),
	BooksSynced: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_books_synced_total",
		Help: "Total number of books successfully synced",
	}),
	BooksSkipped: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_books_skipped_total",
		Help: "Total number of books skipped during sync",
	}),
	ProgressUpdates: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_progress_updates_total",
		Help: "Total number of progress updates",
	}),
	RateLimitHits: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_rate_limit_hits_total",
		Help: "Total number of rate limit hits",
	}),
	APIRequests: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "abs_hardcover_api_requests_total",
		Help: "Total number of API requests by endpoint",
	}, []string{"endpoint", "status_code"}),
	APILatency: promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "abs_hardcover_api_latency_seconds",
		Help: "API request latency in seconds",
		Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0},
	}, []string{"endpoint"}),
	CircuitBreakerTripped: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_circuit_breaker_tripped_total",
		Help: "Total number of times circuit breaker was tripped",
	}),
	Retries: promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "abs_hardcover_request_retries_total",
		Help: "Total number of request retries",
	}, []string{"endpoint"}),
}

// BookMetrics contains all metrics related to book processing
var BookMetrics = struct {
	BooksProcessed prometheus.Counter
	BooksFailed    prometheus.Counter
	ProgressUpdates prometheus.Counter
	StatusUpdates  prometheus.Counter
	OwnershipUpdates prometheus.Counter
}{
	BooksProcessed: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_books_processed_total",
		Help: "Total number of books processed",
	}),
	BooksFailed: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_books_failed_total",
		Help: "Total number of books that failed to sync",
	}),
	ProgressUpdates: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_progress_updates_total",
		Help: "Total number of progress updates",
	}),
	StatusUpdates: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_status_updates_total",
		Help: "Total number of status updates",
	}),
	OwnershipUpdates: promauto.NewCounter(prometheus.CounterOpts{
		Name: "abs_hardcover_ownership_updates_total",
		Help: "Total number of ownership updates",
	}),
}

// Exporter is the Prometheus metrics exporter
var Exporter = prometheus.NewRegistry()

// Register registers all metrics with the Prometheus exporter
func Register() {
	// No need to register metrics as we're using promauto to automatically register them
	// This function is kept for backward compatibility and future use
}
