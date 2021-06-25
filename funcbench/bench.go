// Copyright 2020 The Prometheus Authors
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
	"bytes"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/pkg/errors"
	"golang.org/x/perf/benchstat"
)

// TODO: Add unit test.
type Benchmarker struct {
	logger Logger

	benchmarkArgs       []string
	benchFunc           string
	resultCacheDir      string
	scratchWorkspaceDir string
	enablePerflock      bool

	c    *commander
	repo *git.Repository
}

func newBenchmarker(logger Logger, env Environment, c *commander,
	benchTime, benchTimeout time.Duration,
	resultCacheDir, packagePath string, enablePerflock bool) *Benchmarker {
	return &Benchmarker{
		logger:    logger,
		benchFunc: env.BenchFunc(),
		benchmarkArgs: []string{
			// TODO(bwplotka): Allow memprofiles.
			// 'go test' flags: https://golang.org/cmd/go/#hdr-Testing_flags
			"go test",
			"-mod", "vendor",
			"-run", `"^$"`,
			"-bench", fmt.Sprintf(`"^%s$"`, env.BenchFunc()),
			"-benchmem",
			"-benchtime", benchTime.String(),
			"-timeout", benchTimeout.String(),
			packagePath,
		},
		c:                   c,
		repo:                env.Repo(),
		resultCacheDir:      resultCacheDir,
		scratchWorkspaceDir: "/tmp/funcbench-scratch",
		enablePerflock:      enablePerflock,
	}
}

func (b *Benchmarker) benchOutFileName(commit plumbing.Hash) (string, error) {
	// Sanitize bench func.
	bb := bytes.Buffer{}
	e := base64.NewEncoder(base64.StdEncoding, &bb)
	if _, err := e.Write([]byte(b.benchFunc)); err != nil {
		return "", err
	}
	if err := e.Close(); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s-%s.out", bb.String(), commit.String()), nil
}

func (b *Benchmarker) exec(pkgRoot string, commit plumbing.Hash) (string, error) {
	fileName, err := b.benchOutFileName(commit)
	if err != nil {
		return "", err
	}

	if _, err := ioutil.ReadFile(filepath.Join(b.resultCacheDir, fileName)); err == nil {
		fmt.Println("Found previous results for ", fileName, b.benchFunc, "Reusing.")
		return filepath.Join(b.resultCacheDir, fileName), nil
	}

	// TODO Switch working directory before entering this function.
	benchCmd := []string{"sh", "-c", strings.Join(append([]string{"cd", pkgRoot, "&&"}, b.benchmarkArgs...), " ")}

	b.logger.Println("Executing benchmark command for", commit.String(), "\n", benchCmd)
	if b.enablePerflock {
		benchCmd = append([]string{"perflock"}, benchCmd...)
	}
	out, err := b.c.exec(benchCmd...)
	if err != nil {
		return "", errors.Wrap(err, "benchmark ended with an error.")
	}

	fn := filepath.Join(b.resultCacheDir, fileName)
	if b.resultCacheDir != "" {
		if err := os.MkdirAll(b.resultCacheDir, os.ModePerm); err != nil {
			return "", err
		}
	}
	if err := ioutil.WriteFile(fn, []byte(out), os.ModePerm); err != nil {
		return "", err
	}
	return fn, nil
}

func (b *Benchmarker) compareSubBenchmarks(string) ([]*benchstat.Table, error) {
	// TODO(bwplotka): Implement.
	return nil, errors.New("not implemented")
}

func compareBenchmarks(files ...string) ([]*benchstat.Table, error) {
	c := &benchstat.Collection{
		DeltaTest: benchstat.NoDeltaTest,
	}

	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		if err := c.AddFile(file, f); err != nil {
			return nil, err
		}
	}

	tables := c.Tables()
	if tables == nil {
		return nil, errors.New("didn't match any existing benchmarks")
	}

	return tables, nil
}
