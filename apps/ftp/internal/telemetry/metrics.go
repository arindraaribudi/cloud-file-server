package telemetry

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	SessionsActive = promauto.NewGauge(prometheus.GaugeOpts{Name: "ftp_sessions_active"})
	BytesIn        = promauto.NewCounter(prometheus.CounterOpts{Name: "ftp_bytes_in_total"})
	BytesOut       = promauto.NewCounter(prometheus.CounterOpts{Name: "ftp_bytes_out_total"})
	AuthFailures   = promauto.NewCounterVec(prometheus.CounterOpts{Name: "ftp_auth_failures_total"}, []string{"reason"})
	COSLatency     = promauto.NewHistogramVec(prometheus.HistogramOpts{Name: "cos_request_duration_seconds"}, []string{"op"})
	COSErrors      = promauto.NewCounterVec(prometheus.CounterOpts{Name: "cos_request_errors_total"}, []string{"op"})
	CredRefresh    = promauto.NewCounterVec(prometheus.CounterOpts{Name: "cred_refresh_total"}, []string{"source", "outcome"})
)

func Handler() http.Handler { return promhttp.Handler() }