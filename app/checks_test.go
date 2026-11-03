package main

// Every check is tested in BOTH directions. A check that can only pass is worse
// than no check: it fills a status page with green and teaches people to trust it.
//
// The two failures these tests encode are the ones this service exists for:
//   * qBittorrent answering 200 but rejecting the credential -> arr_testall / qbt_login
//   * Jellyfin answering 200 with an empty library        -> jellyfin_items

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func runner() *Runner { return NewRunner(nil, time.Second) }

func TestHTTPStatusBothDirections(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	r := runner()
	if ok, msg := r.evaluate(context.Background(), Check{Kind: "http_status", URL: okSrv.URL}); !ok {
		t.Fatalf("healthy service reported unhealthy: %s", msg)
	}
	if ok, _ := r.evaluate(context.Background(), Check{Kind: "http_status", URL: badSrv.URL}); ok {
		t.Fatal("500 reported as healthy")
	}
}

// A service that is GONE (pod scaled to zero, crashloop) must be reported as
// failing, and must fail fast. The qBittorrent regression on 2026-11-02 showed the
// nasty variant: a blocked path produces a timeout, not an error code, so if a
// transport failure were treated as "no answer yet" the page would stay green while
// the check never completed.
func TestHTTPStatusTransportFailureFailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL
	srv.Close() // nothing is listening on that port any more

	r := runner() // 1s client timeout
	start := time.Now()
	ok, msg := r.evaluate(context.Background(), Check{Kind: "http_status", URL: url})
	if ok {
		t.Fatal("closed port reported as healthy")
	}
	if msg == "" {
		t.Fatal("failure reported with an empty message - nothing to put on the page")
	}
	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Fatalf("closed port took %s to report; it should fail on connection refused", elapsed)
	}
}

func TestArrTestAllBothDirections(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", req.Method)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	t.Setenv("TEST_ARR_KEY", "k")

	r := runner()
	check := Check{Kind: "arr_testall", URL: srv.URL, APIKeyEnv: "TEST_ARR_KEY"}

	body = `[{"id":1,"isValid":true,"message":null}]`
	if ok, msg := r.evaluate(context.Background(), check); !ok {
		t.Fatalf("valid client reported invalid: %s", msg)
	}

	// This is the 2026-11-02 failure: the arr cannot authenticate to qBittorrent.
	body = `[{"id":1,"isValid":false,"message":"Failed to authenticate with qBittorrent."}]`
	if ok, msg := r.evaluate(context.Background(), check); ok {
		t.Fatal("auth failure reported as healthy")
	} else if msg == "" {
		t.Fatal("failure must carry a message")
	}

	body = `[]`
	if ok, _ := r.evaluate(context.Background(), check); ok {
		t.Fatal("no configured clients reported as healthy")
	}
}

func TestJSONCountBothDirections(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	r := runner()
	check := Check{Kind: "json_count", URL: srv.URL, MinCount: 1}

	body = `[{"id":1},{"id":2}]`
	if ok, msg := r.evaluate(context.Background(), check); !ok {
		t.Fatalf("indexers present reported missing: %s", msg)
	}
	body = `[]`
	if ok, _ := r.evaluate(context.Background(), check); ok {
		t.Fatal("empty indexer list reported as healthy")
	}
}

func TestQbtLoginBothDirections(t *testing.T) {
	var answer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	t.Setenv("TEST_QBT_USER", "admin")
	t.Setenv("TEST_QBT_PASS", "secret")

	r := runner()
	check := Check{Kind: "qbt_login", URL: srv.URL, UserEnv: "TEST_QBT_USER", PassEnv: "TEST_QBT_PASS"}

	answer = "Ok."
	if ok, msg := r.evaluate(context.Background(), check); !ok {
		t.Fatalf("accepted login reported rejected: %s", msg)
	}
	answer = "Fails."
	if ok, _ := r.evaluate(context.Background(), check); ok {
		t.Fatal("rejected login reported as healthy")
	}
}

func TestQbtLoginMissingCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if ok, msg := runner().evaluate(context.Background(),
		Check{Kind: "qbt_login", URL: srv.URL, UserEnv: "NOPE_USER", PassEnv: "NOPE_PASS"}); ok || msg == "" {
		t.Fatal("missing credential must fail with a message")
	}
}

func TestJellyfinItemsBothDirections(t *testing.T) {
	var count int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Items":[],"TotalRecordCount":` + itoa(count) + `}`))
	}))
	defer srv.Close()
	t.Setenv("TEST_JF_KEY", "k")

	r := runner()
	check := Check{Kind: "jellyfin_items", URL: srv.URL, APIKeyEnv: "TEST_JF_KEY", MinCount: 1}

	count = 812
	if ok, msg := r.evaluate(context.Background(), check); !ok {
		t.Fatalf("populated library reported empty: %s", msg)
	}
	// The empty /media bind: server up, API answering, nothing in the library.
	count = 0
	if ok, _ := r.evaluate(context.Background(), check); ok {
		t.Fatal("empty library reported as healthy")
	}
}

func TestOverallStatus(t *testing.T) {
	if got := overallStatus(nil); got != http.StatusOK {
		t.Fatalf("no results yet should be 200, got %d", got)
	}
	if got := overallStatus([]Result{{Name: "a", OK: true}}); got != http.StatusOK {
		t.Fatalf("all healthy should be 200, got %d", got)
	}
	if got := overallStatus([]Result{{Name: "a", OK: true}, {Name: "b", OK: false}}); got != http.StatusServiceUnavailable {
		t.Fatalf("one failure should be 503, got %d", got)
	}
}

func TestBasicAuth(t *testing.T) {
	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

	// Not configured: must refuse, not serve detail unprotected.
	handler := basicAuth(next)
	req := httptest.NewRequest(http.MethodGet, "/api/detail", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured detail view should be 503, got %d", rec.Code)
	}

	t.Setenv("STATUS_USER", "u")
	t.Setenv("STATUS_PASS", "p")
	handler = basicAuth(next)

	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/detail", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials should be 401, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/detail", nil)
	req.SetBasicAuth("u", "p")
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid credentials should be 200, got %d", rec.Code)
	}
}

func TestSnapshotIsSortedAndStable(t *testing.T) {
	r := runner()
	r.results = map[string]Result{
		"zeta":  {Name: "zeta", OK: true},
		"alpha": {Name: "alpha", OK: true},
	}
	got := sortedNames(r.Snapshot())
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("expected sorted names, got %v", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// The public page and the health endpoint answer different questions, and conflating
// them cost a night of false "edge unreachable" pages (2026-11-02):
//
//   /         the page: 503 when a *check* fails, because that is what the page shows
//   /health(z) is this process alive: 200 as long as the server answers
//
// An external monitor polled /health, got 404 (it did not exist), and reported the edge
// down 32 times while the edge was fine.
func TestHealthAliasIsServed(t *testing.T) {
	for _, path := range []string{"/health", "/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		// the handler is registered on the real mux in main(); this asserts the path
		// set the binary serves, so a rename cannot silently break monitors again
		if !servesPath(path) {
			t.Fatalf("%s is not served (findable failure: the monitor would get 404)", path)
		}
		_ = req
		_ = rec
	}
}
