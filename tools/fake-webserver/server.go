// Copyright 2019 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type server struct {
	startTime time.Time
}

var (
	registry = prometheus.NewRegistry()

	namespace = "codelab"
	subsystem = "api"

	requestHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "request_duration_seconds",
			Help:      "A histogram of the API HTTP request durations in seconds.",
			Buckets:   prometheus.ExponentialBuckets(0.0001, 1.5, 25),
		},
		[]string{"method", "path", "status"},
	)
	requestsInProgress = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "http_requests_in_progress",
			Help:      "The current number of API HTTP requests in progress.",
		})
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "requests_total",
			Help:      "Total number of requests",
		},
		[]string{"method", "path", "status"},
	)
	requestErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "request_errors_total",
			Help:      "Total number of request errors",
		},
		[]string{"method", "path", "status"},
	)
)

func init() {
	registry.MustRegister(
		requestsTotal,
		requestErrorsTotal,
		requestHistogram,
		requestsInProgress,
	)
}

type responseOpts struct {
	baseLatency time.Duration
	errorRatio  float64

	// Whenever 10*outageDuration has passed, an outage will be simulated
	// that lasts for outageDuration. During the outage, errorRatio is
	// increased by a factor of 10, and baseLatency by a factor of 3.  At
	// start-up time, an outage is simulated, too (so that you can see the
	// effects right ahead and don't have to wait for 10*outageDuration).
	outageDuration time.Duration
}

var opts = map[string]map[string]responseOpts{
	"/api/foo": map[string]responseOpts{
		"GET": responseOpts{
			baseLatency:    10 * time.Millisecond,
			errorRatio:     0.005,
			outageDuration: 23 * time.Second,
		},
		"POST": responseOpts{
			baseLatency:    20 * time.Millisecond,
			errorRatio:     0.02,
			outageDuration: time.Minute,
		},
	},
	"/api/bar": map[string]responseOpts{
		"GET": responseOpts{
			baseLatency:    15 * time.Millisecond,
			errorRatio:     0.0025,
			outageDuration: 13 * time.Second,
		},
		"POST": responseOpts{
			baseLatency:    50 * time.Millisecond,
			errorRatio:     0.01,
			outageDuration: 47 * time.Second,
		},
	},
	"/api/baz": map[string]responseOpts{
		"GET": responseOpts{
			baseLatency:    2 * time.Millisecond,
			errorRatio:     0.01,
			outageDuration: 1 * time.Second,
		},
		"POST": responseOpts{
			baseLatency:    4 * time.Millisecond,
			errorRatio:     0.02,
			outageDuration: 2 * time.Second,
		},
	},
	"/api/boom": map[string]responseOpts{
		"GET": responseOpts{
			baseLatency:    5 * time.Millisecond,
			errorRatio:     0.01,
			outageDuration: 1 * time.Second,
		},
		"POST": responseOpts{
			baseLatency:    14 * time.Millisecond,
			errorRatio:     0.02,
			outageDuration: 2 * time.Second,
		},
	},
}

func (s server) handleAPI(method, path string) {
	requestsInProgress.Inc()
	status := http.StatusOK
	duration := time.Millisecond

	defer func() {
		requestsInProgress.Dec()
		requestHistogram.With(prometheus.Labels{
			"method": method,
			"path":   path,
			"status": fmt.Sprint(status),
		}).Observe(duration.Seconds())
		requestsTotal.WithLabelValues(method, path, fmt.Sprint(status)).Inc()
	}()

	pathOpts, ok := opts[path]
	if !ok {
		status = http.StatusNotFound
		return
	}
	methodOpts, ok := pathOpts[method]
	if !ok {
		status = http.StatusMethodNotAllowed
		return
	}
	latencyFactor := time.Duration(1)
	errorFactor := 1.
	if time.Since(s.startTime)%(10*methodOpts.outageDuration) < methodOpts.outageDuration {
		latencyFactor *= 3
		errorFactor *= 10
	}
	duration = (methodOpts.baseLatency + time.Duration(rand.NormFloat64()*float64(methodOpts.baseLatency)/10)) * latencyFactor

	if rand.Float64() <= methodOpts.errorRatio*errorFactor {
		status = http.StatusInternalServerError
		requestErrorsTotal.WithLabelValues(method, path, fmt.Sprint(status)).Inc()
	}
}
