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
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/alecthomas/kingpin.v2"
)

func main() {

	cfg := struct {
		portCount                int
		disableProcessMetrics    bool
		disableGoMetrics         bool
		disableMetricCompression bool
		oscillationPeriod        time.Duration
	}{}

	app := kingpin.New(filepath.Base(os.Args[0]), "fake-webserver generates metrics for prometheus")
	app.HelpFlag.Short('h')

	app.Flag("port-count", "Number of sequential ports to serve metrics on, starting at 8080").
		Default("5").
		IntVar(&cfg.portCount)
	app.Flag("oscillation-period", "The duration of the rate oscillation period").
		Default("5m").
		DurationVar(&cfg.oscillationPeriod)
	app.Flag("disable-process-metrics", "Include (potentially expensive) process_* metrics.").
		BoolVar(&cfg.disableProcessMetrics)
	app.Flag("disable-go-metrics", "Include (potentially expensive) go_* metrics.").
		BoolVar(&cfg.disableGoMetrics)
	app.Flag("disable-metrics-compression", "Allow gzip compression of metrics.").
		BoolVar(&cfg.disableMetricCompression)

	kingpin.MustParse(app.Parse(os.Args[1:]))

	if !cfg.disableProcessMetrics {
		registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	}
	if !cfg.disableGoMetrics {
		registry.MustRegister(prometheus.NewGoCollector())
	}

	for i := 0; i < cfg.portCount; i++ {
		mux := http.NewServeMux()
		mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
		mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
		mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
		mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
		mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
		mux.Handle("/metrics", promhttp.HandlerFor(
			registry,
			promhttp.HandlerOpts{
				DisableCompression: cfg.disableMetricCompression,
			},
		))
		go func(i int) {
			log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", 8080+i), mux))
		}(i)
	}
	c := client{
		oscillationPeriod: cfg.oscillationPeriod,
		startTime:         time.Now(),
	}
	c.run()
}
