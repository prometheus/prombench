// Copyright 2017 The Prometheus Authors
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
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	circleci "github.com/jszwedko/go-circleci"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	testJobName  = "test"
	circleJobKey = "CIRCLE_JOB"
	org          = "prometheus"
	repo         = "prometheus"
	binary       = "prometheus"
)

func readTokenFile(name string) string {
	f, err := os.Open(name)
	if err != nil {
		log.Fatal(err)
	}
	b, err := ioutil.ReadAll(f)
	if err != nil {
		log.Fatal(err)
	}
	return strings.Trim(string(b), "\n")
}

func main() {
	log.SetFlags(log.LstdFlags | log.Llongfile)

	app := kingpin.New(filepath.Base(os.Args[0]), "The Prometheus runner tool")
	app.HelpFlag.Short('h')

	var (
		outFile  string
		cctFile  string
		prNumber int
	)
	app.Flag("output.file", "Output file.").Default("prometheus").StringVar(&outFile)
	app.Flag("token.circleci", "CircleCI token file.").Required().ExistingFileVar(&cctFile)
	app.Arg("pr", "Pull request number.").Required().IntVar(&prNumber)

	if _, err := app.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	outFile, err := filepath.Abs(outFile)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("creating CircleCI client")
	cct := readTokenFile(cctFile)
	ccc := circleci.Client{Token: cct}

	log.Printf("querying CircleCI for builds of PR #%d", prNumber)
	builds, err := ccc.ListRecentBuildsForProject(org, repo, fmt.Sprintf("pull/%d", prNumber), "success", 10, 0)
	if err != nil {
		log.Fatalf("fail to find a successfull build: %v", err)
	}
	var build *circleci.Build
	for _, b := range builds {
		if j := b.BuildParameters[circleJobKey]; j == testJobName {
			build = b
			break
		}
	}
	if build == nil {
		log.Fatalf("fail to find a successfull build with job=%q", testJobName)
	}

	log.Printf("querying CircleCI for build #%d artifacts", build.BuildNum)
	artifacts, err := ccc.ListBuildArtifacts(org, repo, build.BuildNum)
	if err != nil {
		log.Fatalf("fail to find artifacts for build %d: %v", build.BuildNum, err)
	}
	var artifact *circleci.Artifact
	for _, a := range artifacts {
		if strings.HasSuffix(a.Path, "/"+binary) {
			artifact = a
			break
		}
	}
	if artifact == nil {
		log.Fatalf("fail to find artifact for build %d", build.BuildNum)
	}

	log.Printf("downloading %q artifact from build %d", artifact.Path, build.BuildNum)
	u, err := url.Parse(artifact.URL)
	p := url.Values{}
	p.Add("circle-token", cct)
	u.RawQuery = p.Encode()

	f, err := os.OpenFile(outFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := http.Get(u.String())
	if err != nil {
		f.Close()
		log.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}

	syscall.Exec(outFile, []string{"prometheus", "--config.file=/etc/prometheus/config/prometheus.yaml", "--storage.tsdb.path=/data"}, os.Environ())
}
