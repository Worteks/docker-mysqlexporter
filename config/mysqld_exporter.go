// Copyright 2018 The Prometheus Authors
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
	"context"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/prometheus/mysqld_exporter/collector"
)

var (
	listenAddress = kingpin.Flag(
		"web.listen-address",
		"Address to listen on for web interface and telemetry.",
	).Default(":9113").String()
	metricPath = kingpin.Flag(
		"web.telemetry-path",
		"Path under which to expose metrics.",
	).Default("/metrics").String()
	timeoutOffset = kingpin.Flag(
		"timeout-offset",
		"Offset to subtract from timeout in seconds.",
	).Default("0.25").Float64()
	dsn string
)

// scrapers lists all possible collection methods and if they should be enabled by default.
var scrapers = map[collector.Scraper]bool{
	collector.ScrapeGlobalStatus{}:                    true,
	collector.ScrapeGlobalVariables{}:                 true,
	collector.ScrapeSlaveStatus{}:                     true,
	collector.ScrapeProcesslist{}:                     false,
	collector.ScrapeUser{}:                            false,
	collector.ScrapeTableSchema{}:                     true,
	collector.ScrapeInfoSchemaInnodbTablespaces{}:     false,
	collector.ScrapeInnodbMetrics{}:                   false,
	collector.ScrapeAutoIncrementColumns{}:            false,
	collector.ScrapeBinlogSize{}:                      false,
	collector.ScrapePerfTableIOWaits{}:                false,
	collector.ScrapePerfIndexIOWaits{}:                false,
	collector.ScrapePerfTableLockWaits{}:              false,
	collector.ScrapePerfEventsStatements{}:            false,
	collector.ScrapePerfEventsWaits{}:                 false,
	collector.ScrapePerfFileEvents{}:                  false,
	collector.ScrapePerfFileInstances{}:               false,
	collector.ScrapePerfReplicationGroupMemberStats{}: false,
	collector.ScrapeUserStat{}:                        false,
	collector.ScrapeClientStat{}:                      false,
	collector.ScrapeTableStat{}:                       false,
	collector.ScrapeInnodbCmp{}:                       true,
	collector.ScrapeInnodbCmpMem{}:                    true,
	collector.ScrapeQueryResponseTime{}:               true,
	collector.ScrapeEngineTokudbStatus{}:              false,
	collector.ScrapeEngineInnodbStatus{}:              false,
	collector.ScrapeHeartbeat{}:                       false,
	collector.ScrapeSlaveHosts{}:                      false,
}

func init() {
	prometheus.MustRegister(version.NewCollector("mysqld_exporter"))
}

func newHandler(metrics collector.Metrics, scrapers []collector.Scraper) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filteredScrapers := scrapers
		params := r.URL.Query()["collect[]"]
		// Use request context for cancellation when connection gets closed.
		ctx := r.Context()
		// If a timeout is configured via the Prometheus header, add it to the context.
		if v := r.Header.Get("X-Prometheus-Scrape-Timeout-Seconds"); v != "" {
			timeoutSeconds, err := strconv.ParseFloat(v, 64)
			if err != nil {
				log.Errorf("Failed to parse timeout from Prometheus header: %s", err)
			} else {
				if *timeoutOffset >= timeoutSeconds {
					// Ignore timeout offset if it doesn't leave time to scrape.
					log.Errorf(
						"Timeout offset (--timeout-offset=%.2f) should be lower than prometheus scrape time (X-Prometheus-Scrape-Timeout-Seconds=%.2f).",
						*timeoutOffset,
						timeoutSeconds,
					)
				} else {
					// Subtract timeout offset from timeout.
					timeoutSeconds -= *timeoutOffset
				}
				// Create new timeout context with request context as parent.
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds*float64(time.Second)))
				defer cancel()
				// Overwrite request with timeout context.
				r = r.WithContext(ctx)
			}
		}
		log.Debugln("collect query:", params)

		// Check if we have some "collect[]" query parameters.
		if len(params) > 0 {
			filters := make(map[string]bool)
			for _, param := range params {
				filters[param] = true
			}

			filteredScrapers = nil
			for _, scraper := range scrapers {
				if filters[scraper.Name()] {
					filteredScrapers = append(filteredScrapers, scraper)
				}
			}
		}

		registry := prometheus.NewRegistry()
		registry.MustRegister(collector.New(ctx, dsn, metrics, filteredScrapers))

		gatherers := prometheus.Gatherers{
			prometheus.DefaultGatherer,
			registry,
		}
		// Delegate http serving to Prometheus client library, which will call collector.Collect.
		h := promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{})
		h.ServeHTTP(w, r)
	}
}

func main() {
	// Generate ON/OFF flags for all scrapers.
	scraperFlags := map[collector.Scraper]*bool{}
	for scraper, enabledByDefault := range scrapers {
		defaultOn := "false"
		if enabledByDefault {
			defaultOn = "true"
		}

		f := kingpin.Flag(
			"collect."+scraper.Name(),
			scraper.Help(),
		).Default(defaultOn).Bool()

		scraperFlags[scraper] = f
	}

	// Parse flags.
	log.AddFlags(kingpin.CommandLine)
	kingpin.Version(version.Print("mysqld_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	var landingPage = []byte(`<html>
<head><title>MySQLd exporter</title></head>
<body>
<h1>MySQLd exporter</h1>
<p><a href='` + *metricPath + `'>Metrics</a></p>
</body>
</html>
`)

	log.Infoln("Starting mysqld_exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	dsn = os.Getenv("DATA_SOURCE_NAME")
	if len(dsn) == 0 {
		var user string
		var pass string
		var host string
		var port string

		if len(os.Getenv("DATA_SOURCE_USER_FILE")) != 0 {
			fileContents, err := ioutil.ReadFile(os.Getenv("DATA_SOURCE_USER_FILE"))
			if err != nil {
				panic(err)
			}
			user = strings.TrimSpace(string(fileContents))
		} else {
			user = os.Getenv("DATA_SOURCE_USER")
		}

		if len(os.Getenv("DATA_SOURCE_PASS_FILE")) != 0 {
			fileContents, err := ioutil.ReadFile(os.Getenv("DATA_SOURCE_PASS_FILE"))
			if err != nil {
				panic(err)
			}
			pass = strings.TrimSpace(string(fileContents))
		} else {
			pass = os.Getenv("DATA_SOURCE_PASS")
		}

		if len(os.Getenv("DATA_SOURCE_HOST_FILE")) != 0 {
			fileContents, err := ioutil.ReadFile(os.Getenv("DATA_SOURCE_HOST_FILE"))
			if err != nil {
				panic(err)
			}
			host = strings.TrimSpace(string(fileContents))
		} else {
			host = os.Getenv("DATA_SOURCE_HOST")
		}
		if len(host) == 0 {
			host = "127.0.0.1"
		}

		if len(os.Getenv("DATA_SOURCE_PORT_FILE")) != 0 {
			fileContents, err := ioutil.ReadFile(os.Getenv("DATA_SOURCE_PORT_FILE"))
			if err != nil {
				panic(err)
			}
			port = strings.TrimSpace(string(fileContents))
		} else {
			port = os.Getenv("DATA_SOURCE_PORT")
		}
		if len(port) == 0 {
			port = "3306"
		}
		if len(user) > 0 {
			dsn = user + ":" + pass + "@" + host + ":" + port
		} else {
			dsn = host + ":" + port
		}
	}

	// Register only scrapers enabled by flag.
	log.Infof("Enabled scrapers:")
	enabledScrapers := []collector.Scraper{}
	for scraper, enabled := range scraperFlags {
		if *enabled {
			log.Infof(" --collect.%s", scraper.Name())
			enabledScrapers = append(enabledScrapers, scraper)
		}
	}
	handlerFunc := newHandler(collector.NewMetrics(), enabledScrapers)
	http.HandleFunc(*metricPath, prometheus.InstrumentHandlerFunc("metrics", handlerFunc))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(landingPage)
	})

	log.Infoln("Listening on", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
