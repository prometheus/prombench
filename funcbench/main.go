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
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"golang.org/x/perf/benchstat"
	"gopkg.in/alecthomas/kingpin.v2"
)

type Logger interface {
	Println(v ...interface{})
}

type logger struct {
	*log.Logger

	verbose bool
}

func (l *logger) FatalError(err error) {
	if l.verbose {
		l.Fatalf("%+v", err)
	}
	l.Fatalf("%v", err)
}

func main() {
	cfg := struct {
		userTestName   string
		verbose        bool
		nocomment      bool
		owner          string
		repo           string
		resultsDir     string
		workspaceDir   string
		ghPR           int
		benchTime      time.Duration
		benchTimeout   time.Duration
		compareTarget  string
		benchFuncRegex string
		packagePath    string
		enablePerflock bool
	}{}

	app := kingpin.New(
		filepath.Base(os.Args[0]),
		`Benchmark and compare your Go code between sub benchmarks or commits.
		* For BenchmarkFuncName, compare current with master: ./funcbench -v master BenchmarkFuncName
		* For BenchmarkFunc.*, compare current with master: ./funcbench -v master BenchmarkFunc.*
		* For all benchmarks, compare current with devel: ./funcbench -v devel .* or ./funcbench -v devel
		* For BenchmarkFunc.*, compare current with 6d280 commit: ./funcbench -v 6d280 BenchmarkFunc.*
		* For BenchmarkFunc.*, compare between sub-benchmarks of same benchmark on current commit: ./funcbench -v . BenchmarkFunc.*
		* For BenchmarkFuncName, compare pr#35 with master: ./funcbench --nocomment --github-pr="35" master BenchmarkFuncName`,
	)
	// Options.
	app.HelpFlag.Short('h')
	app.Flag("verbose", "Verbose mode. Errors includes trace and commands output are logged.").
		Short('v').BoolVar(&cfg.verbose)
	app.Flag("nocomment", "Disable posting of comment using the GitHub API.").
		BoolVar(&cfg.nocomment)

	app.Flag("owner", "A Github owner or organisation name.").
		Default("prometheus").StringVar(&cfg.owner)
	app.Flag("repo", "This is the repository name.").
		Default("prometheus").StringVar(&cfg.repo)
	app.Flag("github-pr", "GitHub PR number to pull changes from and to post benchmark results.").
		IntVar(&cfg.ghPR)
	app.Flag("workspace", "Directory to clone GitHub PR.").
		Default("/tmp/funcbench").
		StringVar(&cfg.workspaceDir)
	app.Flag("result-cache", "Directory to store benchmark results.").
		Default("funcbench-results").
		StringVar(&cfg.resultsDir)
	app.Flag("user-test-name", "Name of the test to keep track of multiple benchmarks").
		Default("default").
		Short('n').
		StringVar(&cfg.userTestName)

	app.Flag("bench-time", "Run enough iterations of each benchmark to take t, specified "+
		"as a time.Duration. The special syntax Nx means to run the benchmark N times").
		Short('t').Default("1s").DurationVar(&cfg.benchTime)
	app.Flag("timeout", "Benchmark timeout specified in time.Duration format, "+
		"disabled if set to 0. If a test binary runs longer than duration d, panic.").
		Short('d').Default("2h").DurationVar(&cfg.benchTimeout)
	app.Flag("perflock", "Enable perflock (you must have perflock installed to use this)").
		Short('l').
		Default("false").
		BoolVar(&cfg.enablePerflock)

	app.Arg("target", "Can be one of '.', tag name, branch name or commit SHA of the branch "+
		"to compare against. If set to '.', branch/commit is the same as the current one; "+
		"funcbench will run once and try to compare between 2 sub-benchmarks. "+
		"Errors out if there are no sub-benchmarks.").
		Required().StringVar(&cfg.compareTarget)
	app.Arg("bench-func-regex", "Function regex to use for benchmark."+
		"Supports RE2 regexp and is fully anchored, by default will run all benchmarks.").
		Default(".*").
		StringVar(&cfg.benchFuncRegex) // TODO (geekodour) : validate regex?
	app.Arg("packagepath", "Package to run benchmark against. Eg. ./tsdb, defaults to ./...").
		Default("./...").
		StringVar(&cfg.packagePath)

	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := &logger{
		// Show file line with each log.
		Logger:  log.New(os.Stdout, "funcbech", log.Ltime|log.Lshortfile),
		verbose: cfg.verbose,
	}

	var g run.Group
	// Main routine.
	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			var (
				env Environment
				err error
			)

			// Setup Environment.
			e := environment{
				logger:        logger,
				benchFunc:     cfg.benchFuncRegex,
				compareTarget: cfg.compareTarget,
			}
			if cfg.ghPR == 0 {
				// Local Mode.
				env, err = newLocalEnv(e)
				if err != nil {
					return errors.Wrap(err, "environment create")
				}
			} else {
				// Github Mode.
				ghClient, err := newGitHubClient(ctx, cfg.owner, cfg.repo, cfg.ghPR, cfg.nocomment)
				if err != nil {
					return errors.Wrapf(err, "github client")
				}

				env, err = newGitHubEnv(ctx, e, ghClient, cfg.workspaceDir)
				if err != nil {
					if err := ghClient.postComment(fmt.Sprintf("%v. Could not setup environment, please check logs.", err)); err != nil {
						return errors.Wrap(err, "could not post error")
					}
					return errors.Wrap(err, "environment create")
				}
			}

			// ( ◔_◔)ﾉ Start benchmarking!
			benchmarker := newBenchmarker(logger, env,
				&commander{verbose: cfg.verbose, ctx: ctx},
				cfg.benchTime, cfg.benchTimeout,
				path.Join(cfg.resultsDir, cfg.userTestName),
				cfg.packagePath,
				cfg.enablePerflock,
			)
			tables, err := startBenchmark(env, benchmarker)
			if err != nil {
				pErr := env.PostErr(
					fmt.Sprintf(
						"```\n%s\n```\nError:\n```\n%s\n```",
						strings.Join(benchmarker.benchmarkArgs, " "),
						err.Error(),
					),
				)

				if pErr != nil {
					return errors.Wrap(pErr, "could not log error")
				}
				return err
			}

			// Post results.
			// TODO (geekodour): probably post some kind of funcbench summary(?)
			return env.PostResults(
				tables,
				fmt.Sprintf("```\n%s\n```", strings.Join(benchmarker.benchmarkArgs, " ")),
			)

		}, func(err error) {
			cancel()
		})
	}
	// Listen for termination signals.
	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return interrupt(logger, cancel)
		}, func(error) {
			close(cancel)
		})
	}

	if err := g.Run(); err != nil {
		logger.FatalError(errors.Wrap(err, "running command failed"))
	}
	logger.Println("exiting")
}

// startBenchmark returns the comparision results.
// 1. If target is same as current ref, run sub-benchmarks and return instead (TODO).
// 2. Execute benchmark against packages in the current worktree.
// 3. Cleanup of worktree in case funcbench was run previously and checkout target worktree.
// 4. Execute benchmark against packages in the new(target) worktree.
// 5. Return compared results.
func startBenchmark(env Environment, bench *Benchmarker) ([]*benchstat.Table, error) {

	wt, _ := env.Repo().Worktree()
	cmpWorkTreeDir := filepath.Join(bench.scratchWorkspaceDir)

	ref, err := env.Repo().Head()
	if err != nil {
		return nil, errors.Wrap(err, "get head")
	}

	// TODO move it into env? since GitHub env doesn't need this check.
	if _, err := bench.c.exec("sh", "-c", "git update-index -q --ignore-submodules --refresh && git diff-files --quiet --ignore-submodules --"); err != nil {
		return nil, errors.Wrap(err, "not clean worktree")
	}

	if env.CompareTarget() == "." {
		bench.logger.Println("Assuming sub-benchmarks comparison.")
		subResult, err := bench.exec(wt.Filesystem.Root(), ref.Hash())
		if err != nil {
			return nil, errors.Wrap(err, "execute sub-benchmark")
		}

		cmps, err := bench.compareSubBenchmarks(subResult)
		if err != nil {
			return nil, errors.Wrap(err, "comparing sub benchmarks")
		}
		return cmps, nil
	}

	// Get info about target.
	targetCommit := getTargetInfo(env.Repo(), env.CompareTarget())
	if targetCommit == plumbing.ZeroHash {
		return nil, fmt.Errorf("cannot find target %s", env.CompareTarget())
	}

	bench.logger.Println("Target:", targetCommit.String(), "Current Ref:", ref.Hash().String())

	if targetCommit == ref.Hash() {
		return nil, fmt.Errorf("target: %s is the same as current ref %s (or is on the same commit); No changes would be expected; Aborting", targetCommit, ref.String())
	}

	bench.logger.Println("Assuming comparing with target (clean workdir will be checked.)")

	// Execute benchmark A.
	newResult, err := bench.exec(wt.Filesystem.Root(), ref.Hash())
	if err != nil {
		return nil, errors.Wrapf(err, "execute benchmark for A: %v", ref.Name().String())
	}

	// TODO move the following part before 'Execute benchmark B.' into a function Benchmarker.switchToWorkTree.
	// Best effort cleanup and checkout new worktree.
	if err := os.RemoveAll(cmpWorkTreeDir); err != nil {
		return nil, errors.Wrapf(err, "delete worktree at %s", cmpWorkTreeDir)
	}

	// TODO (geekodour): switch to worktree remove once we decide not to support git<2.17
	if _, err := bench.c.exec("git", "worktree", "prune"); err != nil {
		return nil, errors.Wrap(err, "worktree prune")
	}

	bench.logger.Println("Checking out (in new workdir):", cmpWorkTreeDir, "commmit", targetCommit.String())
	if _, err := bench.c.exec("git", "worktree", "add", "-f", cmpWorkTreeDir, targetCommit.String()); err != nil {
		return nil, errors.Wrapf(err, "checkout %s in worktree %s", targetCommit.String(), cmpWorkTreeDir)
	}

	// Execute benchmark B.
	oldResult, err := bench.exec(cmpWorkTreeDir, targetCommit)
	if err != nil {
		return nil, errors.Wrapf(err, "execute benchmark for B: %v", env.CompareTarget())
	}

	// Compare B vs A.
	tables, err := compareBenchmarks(oldResult, newResult)
	if err != nil {
		return nil, errors.Wrap(err, "comparing benchmarks")
	}

	// Save hashes for info about benchmark.
	env.SetHashStrings(targetCommit.String(), ref.Hash().String())

	return tables, nil
}

func interrupt(logger Logger, cancel <-chan struct{}) error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-c:
		logger.Println("caught signal", s, "Exiting.")
		return nil
	case <-cancel:
		return errors.New("canceled")
	}
}

// getTargetInfo returns the hash of the target if found,
// otherwise returns plumbing.ZeroHash.
// NOTE: if both a branch and a tag have the same name, it always chooses the branch name.
func getTargetInfo(repo *git.Repository, target string) plumbing.Hash {
	hash, err := repo.ResolveRevision(plumbing.Revision(target))
	if err != nil {
		return plumbing.ZeroHash
	}
	return *hash
}

type commander struct {
	verbose bool
	ctx     context.Context
}

func (c *commander) exec(command ...string) (string, error) {
	cmd := exec.CommandContext(c.ctx, command[0], command[1:]...)
	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b

	if c.verbose {
		// All to stdout.
		cmd.Stdout = io.MultiWriter(cmd.Stdout, os.Stdout)
		cmd.Stderr = io.MultiWriter(cmd.Stdout, os.Stdout)
	}
	if err := cmd.Run(); err != nil {
		out := b.String()
		return "", errors.Errorf("error: %v; Command out: %s", err, out)
	}

	return b.String(), nil
}
