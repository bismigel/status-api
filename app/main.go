package main

// status-api: one place that knows how to check each service properly, and two
// views of the same data.
//
//	/                     aggregate for the public page: service name + ok/fail only
//	/api/status           same, as JSON (no URLs, no error text)
//	/api/detail           the same data WITH messages — basic auth
//	/metrics              Prometheus exposition, cluster-only (not routed by the Ingress)
//	/healthz              this process is alive
//
// The split matters: the page is public, so it must not leak internal URLs or error
// strings; the operator view needs exactly those. Prometheus needs machine-readable
// series, and must not be reachable from the internet at all.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"
)

func main() {
	checksFile := getenv("CHECKS_FILE", "/etc/status/checks.json")
	pollSeconds, _ := strconv.Atoi(getenv("POLL_SECONDS", "60"))
	if pollSeconds < 10 {
		pollSeconds = 10
	}

	checks, err := LoadChecks(checksFile)
	if err != nil {
		// Fail loudly at startup: a status API that cannot state what it checks is
		// worse than one that refuses to run.
		log.Fatalf("status-api: %v", err)
	}
	log.Printf("status-api: %d check(s) from %s, every %ds", len(checks), checksFile, pollSeconds)

	runner := NewRunner(checks, time.Duration(pollSeconds)*time.Second)
	go runner.Start(context.Background())

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		results := runner.Snapshot()
		// Public shape: name + ok only. No messages: they quote internal URLs and
		// backend error strings.
		type public struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		}
		out := struct {
			Status         int       `json:"status"`       // 200 or 503
			Checks         []public  `json:"checks"`
			Failing        int       `json:"failing"`
			GeneratedAt    time.Time `json:"generated_at"`
		}{
			Status:      overallStatus(results),
			GeneratedAt: time.Now().UTC(),
		}
		for _, res := range results {
			out.Checks = append(out.Checks, public{Name: res.Name, OK: res.OK})
			if !res.OK {
				out.Failing++
			}
		}
		if out.Status != http.StatusOK {
			w.WriteHeader(out.Status)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/detail", basicAuth(func(w http.ResponseWriter, r *http.Request) {
		results := runner.Snapshot()
		payload := struct {
			Status      int       `json:"status"`
			Checks      []Result  `json:"checks"`
			Failing     int       `json:"failing"`
			GeneratedAt time.Time `json:"generated_at"`
		}{Status: overallStatus(results), Checks: results, GeneratedAt: time.Now().UTC()}
		for _, res := range results {
			if !res.OK {
				payload.Failing++
			}
		}
		if payload.Status != http.StatusOK {
			w.WriteHeader(payload.Status)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
	}))

	// Metrics live on their own listener. The Ingress routes the service's public
	// port only, so /metrics cannot be reached from the internet, and Prometheus
	// scrapes it in-cluster without needing credentials on the scrape path.
	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		results := runner.Snapshot()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintln(w, "# HELP status_api_check_ok 1 when the check passed, 0 when it failed")
		fmt.Fprintln(w, "# TYPE status_api_check_ok gauge")
		for _, res := range results {
			val := 0
			if res.OK {
				val = 1
			}
			fmt.Fprintf(w, "status_api_check_ok{check=%q,kind=%q} %d\n", res.Name, res.Kind, val)
		}
		fmt.Fprintln(w, "# HELP status_api_check_latency_ms how long the check took")
		fmt.Fprintln(w, "# TYPE status_api_check_latency_ms gauge")
		for _, res := range results {
			fmt.Fprintf(w, "status_api_check_latency_ms{check=%q} %d\n", res.Name, res.LatencyMS)
		}
		fmt.Fprintln(w, "# HELP status_api_last_check_seconds unix time of the last result")
		fmt.Fprintln(w, "# TYPE status_api_last_check_seconds gauge")
		for _, res := range results {
			fmt.Fprintf(w, "status_api_last_check_seconds{check=%q} %d\n", res.Name, res.CheckedAt.Unix())
		}
		failing := 0
		for _, res := range results {
			if !res.OK {
				failing++
			}
		}
		fmt.Fprintln(w, "# HELP status_api_checks_total declared checks")
		fmt.Fprintln(w, "# TYPE status_api_checks_total gauge")
		fmt.Fprintf(w, "status_api_checks_total %d\n", len(results))
		fmt.Fprintln(w, "# HELP status_api_checks_failing checks currently failing")
		fmt.Fprintln(w, "# TYPE status_api_checks_failing gauge")
		fmt.Fprintf(w, "status_api_checks_failing %d\n", failing)
		fmt.Fprintln(w, "# HELP status_api_overall 1 when every check passes")
		fmt.Fprintln(w, "# TYPE status_api_overall gauge")
		if failing == 0 {
			fmt.Fprintln(w, "status_api_overall 1")
		} else {
			fmt.Fprintln(w, "status_api_overall 0")
		}
	})

	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	metricsAddr := ":" + getenv("METRICS_PORT", "9091")
	go func() {
		log.Printf("status-api: metrics on %s", metricsAddr)
		log.Fatal((&http.Server{Addr: metricsAddr, Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe())
	}()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	addr := ":" + getenv("PORT", "8080")
	log.Printf("status-api: listening on %s", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// overallStatus returns 200 when every check passes (or none has run yet), else 503
// so that an external probe doing `HTTP 200` gets a truthful answer.
func overallStatus(results []Result) int {
	if len(results) == 0 {
		return http.StatusOK // not yet polled; the readiness probe covers startup
	}
	for _, res := range results {
		if !res.OK {
			return http.StatusServiceUnavailable
		}
	}
	return http.StatusOK
}

// basicAuth guards the detail view. If no credentials are configured it refuses
// rather than serving detail unprotected — a misconfigured secret must not turn
// into an information leak.
func basicAuth(next http.HandlerFunc) http.HandlerFunc {
	user, pass := os.Getenv("STATUS_USER"), os.Getenv("STATUS_PASS")
	return func(w http.ResponseWriter, r *http.Request) {
		if user == "" || pass == "" {
			http.Error(w, "detail view is not configured", http.StatusServiceUnavailable)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="status-api detail"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// sortedNames is used by the tests and keeps the public listing stable.
func sortedNames(results []Result) []string {
	names := make([]string, 0, len(results))
	for _, res := range results {
		names = append(names, res.Name)
	}
	sort.Strings(names)
	return names
}
