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
	"math"
	"time"
)

type client struct {
	oscillationPeriod time.Duration
	startTime         time.Time
}

func (c client) oscillationFactor() float64 {
	// Source: https://github.com/prometheus/test-infra/pull/3
	return 2 + math.Sin(math.Sin(2*math.Pi*float64(time.Since(c.startTime))/float64(c.oscillationPeriod)))
}

func (c client) run() {

	s := server{startTime: c.startTime}

	// GET /api/foo.
	go func() {
		for {
			s.handleAPI("GET", "/api/foo")
			time.Sleep(time.Duration(3*c.oscillationFactor()) * time.Millisecond)
		}
	}()
	// POST /api/foo.
	go func() {
		for {
			s.handleAPI("POST", "/api/foo")
			time.Sleep(time.Duration(25*c.oscillationFactor()) * time.Millisecond)
		}
	}()
	// GET /api/bar.
	go func() {
		for {
			s.handleAPI("GET", "/api/bar")
			time.Sleep(time.Duration(10*c.oscillationFactor()) * time.Millisecond)
		}
	}()
	// POST /api/bar.
	go func() {
		for {
			s.handleAPI("POST", "/api/bar")
			time.Sleep(time.Duration(5*c.oscillationFactor()) * time.Millisecond)
		}
	}()
	// GET /api/baz.
	go func() {
		for {
			s.handleAPI("POST", "/api/baz")
			time.Sleep(time.Duration(70*c.oscillationFactor()) * time.Millisecond)
		}
	}()
	// GET /api/boom.
	go func() {
		for {
			s.handleAPI("GET", "/api/boom")
			time.Sleep(time.Duration(80*c.oscillationFactor()) * time.Millisecond)
		}
	}()
	// GET /api/nonexistent.
	go func() {
		for {
			s.handleAPI("POST", "/api/boom")
			time.Sleep(time.Duration(90*c.oscillationFactor()) * time.Millisecond)
		}
	}()

	select {}
}
