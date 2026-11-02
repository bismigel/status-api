package main

// Checks: what "the service works" actually means, per service.
//
// This file exists because a running pod is not a working app. On 2026-11-02 two
// services answered HTTP 200 while being functionally dead: qBittorrent had no
// WebUI password (so the *arrs could not authenticate to it) and Jellyfin had an
// empty /media bind (so it served an empty library). Every check below exists to
// catch that class of failure — the app is up, the thing it does is broken.
//
// Design rules:
//   * every check must be able to FAIL — a check that cannot fail is decoration,
//     and each one is tested in both directions in checks_test.go
//   * results are cached: the HTTP handlers never wait on a service, so a slow or
//     hung backend cannot make the status page hang with it
//   * expensive checks declare their own interval (downloadclient/testall logs in
//     to qBittorrent; a library count walks Jellyfin's index)

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Check is one assertion about one service, as declared in checks.json.
type Check struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	UserEnv   string `json:"user_env,omitempty"`
	PassEnv   string `json:"pass_env,omitempty"`
	MinCount  int    `json:"min_count,omitempty"`
	// ExpectStatus defaults to 2xx.
	ExpectStatus []int `json:"expect_status,omitempty"`
	// BodyContains asserts a substring of the response body (qBittorrent says "Ok.").
	BodyContains string `json:"body_contains,omitempty"`
	// Interval overrides the global poll interval, for expensive checks.
	Interval int `json:"interval_seconds,omitempty"`
	Timeout  int `json:"timeout_seconds,omitempty"`
}

// Result is the cached outcome of one check.
type Result struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	OK        bool      `json:"ok"`
	LatencyMS int64     `json:"latency_ms"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Runner runs every declared check on its own schedule and caches the outcome.
type Runner struct {
	checks  []Check
	client  *http.Client
	mu      sync.RWMutex
	results map[string]Result
	// defaultInterval applies to checks that do not declare one.
	defaultInterval time.Duration
}

func NewRunner(checks []Check, defaultInterval time.Duration) *Runner {
	return &Runner{
		checks:          checks,
		client:          &http.Client{Timeout: 10 * time.Second},
		results:         make(map[string]Result),
		defaultInterval: defaultInterval,
	}
}

// LoadChecks reads the declarative check list.
func LoadChecks(path string) ([]Check, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Checks []Check `json:"checks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Checks) == 0 {
		return nil, fmt.Errorf("%s declares no checks", path)
	}
	return doc.Checks, nil
}

// Start runs every check immediately, then on its interval, until ctx is done.
func (r *Runner) Start(ctx context.Context) {
	for _, c := range r.checks {
		c := c
		go func() {
			interval := r.defaultInterval
			if c.Interval > 0 {
				interval = time.Duration(c.Interval) * time.Second
			}
			for {
				res := r.run(ctx, c)
				r.mu.Lock()
				r.results[c.Name] = res
				r.mu.Unlock()
				select {
				case <-ctx.Done():
					return
				case <-time.After(interval):
				}
			}
		}()
	}
}

// Snapshot returns the cached results, sorted by name.
func (r *Runner) Snapshot() []Result {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Result, 0, len(r.results))
	for _, res := range r.results {
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// run executes one check and always returns a Result, even on panic, so one bad
// check can never take the whole status page down with it.
func (r *Runner) run(ctx context.Context, c Check) (res Result) {
	res = Result{Name: c.Name, Kind: c.Kind, CheckedAt: time.Now()}
	defer func() {
		if p := recover(); p != nil {
			res.OK = false
			res.Message = fmt.Sprintf("check panicked: %v", p)
		}
	}()
	timeout := 8 * time.Second
	if c.Timeout > 0 {
		timeout = time.Duration(c.Timeout) * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	ok, msg := r.evaluate(cctx, c)
	res.LatencyMS = time.Since(start).Milliseconds()
	res.OK = ok
	res.Message = msg
	return res
}

func (r *Runner) evaluate(ctx context.Context, c Check) (bool, string) {
	switch c.Kind {
	case "http_status":
		return r.httpStatus(ctx, c)
	case "arr_testall":
		return r.arrTestAll(ctx, c)
	case "json_count":
		return r.jsonCount(ctx, c)
	case "qbt_login":
		return r.qbtLogin(ctx, c)
	case "jellyfin_items":
		return r.jellyfinItems(ctx, c)
	default:
		return false, fmt.Sprintf("unknown check kind %q", c.Kind)
	}
}

func (r *Runner) do(ctx context.Context, method, target string, apiKey string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return 0, nil, err
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, err
}

// httpStatus is the cheap liveness check: does it answer, and with what.
func (r *Runner) httpStatus(ctx context.Context, c Check) (bool, string) {
	code, body, err := r.do(ctx, http.MethodGet, c.URL, os.Getenv(c.APIKeyEnv))
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", trimErr(err))
	}
	if len(c.ExpectStatus) > 0 {
		for _, want := range c.ExpectStatus {
			if code == want {
				if c.BodyContains != "" && !strings.Contains(string(body), c.BodyContains) {
					return false, fmt.Sprintf("HTTP %d but body lacks %q", code, c.BodyContains)
				}
				return true, fmt.Sprintf("HTTP %d", code)
			}
		}
		return false, fmt.Sprintf("HTTP %d, wanted one of %v", code, c.ExpectStatus)
	}
	if code < 200 || code > 299 {
		return false, fmt.Sprintf("HTTP %d", code)
	}
	if c.BodyContains != "" && !strings.Contains(string(body), c.BodyContains) {
		return false, fmt.Sprintf("HTTP %d but body lacks %q", code, c.BodyContains)
	}
	return true, fmt.Sprintf("HTTP %d", code)
}

// arrTestAll is the check that would have caught the qBittorrent password bug: the
// arr really logs in to its download client, so a broken credential fails here and
// nowhere else.
func (r *Runner) arrTestAll(ctx context.Context, c Check) (bool, string) {
	key := os.Getenv(c.APIKeyEnv)
	if key == "" {
		return false, fmt.Sprintf("%s is not set", c.APIKeyEnv)
	}
	target := strings.TrimRight(c.URL, "/") + "/api/v3/downloadclient/testall?apikey=" + url.QueryEscape(key)
	code, body, err := r.do(ctx, http.MethodPost, target, "")
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", trimErr(err))
	}
	if code != http.StatusOK {
		return false, fmt.Sprintf("HTTP %d", code)
	}
	var results []struct {
		ID      int    `json:"id"`
		IsValid bool   `json:"isValid"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return false, fmt.Sprintf("unparseable response: %v", err)
	}
	if len(results) == 0 {
		return false, "no download clients configured"
	}
	for _, res := range results {
		if !res.IsValid {
			return false, fmt.Sprintf("client %d invalid: %s", res.ID, firstLine(res.Message))
		}
	}
	return true, fmt.Sprintf("%d client(s) valid", len(results))
}

// jsonCount asserts a JSON array has at least MinCount entries (indexers, etc).
func (r *Runner) jsonCount(ctx context.Context, c Check) (bool, string) {
	code, body, err := r.do(ctx, http.MethodGet, c.URL, os.Getenv(c.APIKeyEnv))
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", trimErr(err))
	}
	if code != http.StatusOK {
		return false, fmt.Sprintf("HTTP %d", code)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return false, fmt.Sprintf("not a JSON array: %v", err)
	}
	if len(items) < c.MinCount {
		return false, fmt.Sprintf("%d item(s), want at least %d", len(items), c.MinCount)
	}
	return true, fmt.Sprintf("%d item(s)", len(items))
}

// qbtLogin proves the stored credential still works. A missing password makes
// qBittorrent hand out a temporary one at startup, which no client knows.
func (r *Runner) qbtLogin(ctx context.Context, c Check) (bool, string) {
	user := os.Getenv(c.UserEnv)
	pass := os.Getenv(c.PassEnv)
	if user == "" || pass == "" {
		return false, fmt.Sprintf("%s/%s not set", c.UserEnv, c.PassEnv)
	}
	form := url.Values{"username": {user}, "password": {pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.URL, "/")+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", trimErr(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	// qBittorrent answers "Ok." on success and "Fails." on a bad credential.
	if strings.Contains(string(body), "Ok.") {
		return true, "login accepted"
	}
	return false, fmt.Sprintf("login rejected: %s", firstLine(string(body)))
}

// jellyfinItems is the check that would have caught the empty /media bind: the
// server was up and its API answered while the library was empty.
func (r *Runner) jellyfinItems(ctx context.Context, c Check) (bool, string) {
	key := os.Getenv(c.APIKeyEnv)
	if key == "" {
		return false, fmt.Sprintf("%s is not set", c.APIKeyEnv)
	}
	// Jellyfin rejects api_key in the query (401) and accepts the legacy
	// MediaBrowser token header. Verified against the live server 2026-11-02:
	// query -> 401, X-Emby-Token -> 401, this header -> 200.
	target := strings.TrimRight(c.URL, "/") + "/Items?Recursive=true&Limit=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Authorization", `MediaBrowser Token="`+key+`"`)
	resp, err := r.client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", trimErr(err))
	}
	defer resp.Body.Close()
	code := resp.StatusCode
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", trimErr(err))
	}
	if code != http.StatusOK {
		return false, fmt.Sprintf("HTTP %d", code)
	}
	var doc struct {
		TotalRecordCount int `json:"TotalRecordCount"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, fmt.Sprintf("unparseable response: %v", err)
	}
	if doc.TotalRecordCount < c.MinCount {
		return false, fmt.Sprintf("%d item(s) in the library, want at least %d", doc.TotalRecordCount, c.MinCount)
	}
	return true, fmt.Sprintf("%d item(s)", doc.TotalRecordCount)
}

func trimErr(err error) string { return firstLine(strings.TrimSpace(err.Error())) }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
