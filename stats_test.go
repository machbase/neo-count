package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func testSnapshot(date string, count int64) *snapshot {
	start := testTime(date + "T00:07:00+09:00")
	return &snapshot{
		Version: 1, Owner: statsOwner, Repo: statsRepo,
		StartedAt: start, EndedAt: start.Add(time.Minute),
		Releases: []*Release{{
			ID: 1, TagName: "v1", PublishedAt: "2025-01-01T00:00:00+09:00",
			Assets: []*Asset{{ID: 1, Name: "neo-server-v1-linux-amd64.zip", DownloadCount: count, CreatedAt: "2025-01-01T00:00:00+09:00"}},
		}},
	}
}

func TestSnapshotStoreLatestAndWindow(t *testing.T) {
	dir := t.TempDir()
	s := testSnapshot("2026-01-02", 10)
	if changed, err := storeSnapshot(dir, s); err != nil || !changed {
		t.Fatalf("first store: changed=%v err=%v", changed, err)
	}
	if changed, err := storeSnapshot(dir, s); err != nil || changed {
		t.Fatalf("duplicate store: changed=%v err=%v", changed, err)
	}
	later := testSnapshot("2026-01-02", 15)
	later.StartedAt = later.StartedAt.Add(time.Minute)
	later.EndedAt = later.EndedAt.Add(time.Minute)
	if changed, err := storeSnapshot(dir, later); err != nil || !changed {
		t.Fatalf("later store: changed=%v err=%v", changed, err)
	}
	if changed, err := storeSnapshot(dir, s); err != nil || changed {
		t.Fatalf("older store: changed=%v err=%v", changed, err)
	}
	path := filepath.Join(dir, "snapshots", "2026-01-02.json")
	got, err := loadSnapshot(path)
	if err != nil || got.Releases[0].Assets[0].DownloadCount != 15 {
		t.Fatalf("load latest: snapshot=%v err=%v", got, err)
	}
	late := testSnapshot("2026-01-02", 20)
	late.StartedAt = testTime("2026-01-02T08:19:00+09:00")
	late.EndedAt = testTime("2026-01-02T08:20:00+09:00")
	if changed, err := storeSnapshot(dir, late); err != nil || changed {
		t.Fatalf("late snapshot replaced an on-time snapshot: changed=%v err=%v", changed, err)
	}
	got, err = loadSnapshot(path)
	if err != nil || got.Releases[0].Assets[0].DownloadCount != 15 {
		t.Fatal("late collection changed the stored snapshot")
	}
}

func TestSnapshotStoreLatePrefersEarliest(t *testing.T) {
	dir := t.TempDir()
	late := testSnapshot("2026-01-02", 20)
	late.StartedAt = testTime("2026-01-02T08:19:00+09:00")
	late.EndedAt = testTime("2026-01-02T08:20:00+09:00")
	if changed, err := storeSnapshot(dir, late); err != nil || !changed {
		t.Fatalf("late snapshot: changed=%v err=%v", changed, err)
	}
	later := *late
	later.StartedAt = late.StartedAt.Add(time.Hour)
	later.EndedAt = late.EndedAt.Add(time.Hour)
	if changed, err := storeSnapshot(dir, &later); err != nil || changed {
		t.Fatalf("later snapshot: changed=%v err=%v", changed, err)
	}
	earlier := *late
	earlier.StartedAt = late.StartedAt.Add(-time.Hour)
	earlier.EndedAt = late.EndedAt.Add(-time.Hour)
	if changed, err := storeSnapshot(dir, &earlier); err != nil || !changed {
		t.Fatalf("earlier late snapshot: changed=%v err=%v", changed, err)
	}
	onTime := testSnapshot("2026-01-02", 15)
	if changed, err := storeSnapshot(dir, onTime); err != nil || !changed {
		t.Fatalf("on-time snapshot: changed=%v err=%v", changed, err)
	}
	crossMidnight := *late
	crossMidnight.EndedAt = testTime("2026-01-03T00:00:00+09:00")
	if _, err := storeSnapshot(dir, &crossMidnight); err == nil {
		t.Fatal("cross-midnight snapshot accepted")
	}
}

func TestSnapshotMissingDownloadCount(t *testing.T) {
	dir := t.TempDir()
	s := testSnapshot("2026-01-02", 10)
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"DownloadCount":10,`), nil, 1)
	path := filepath.Join(dir, "2026-01-02.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSnapshot(path); err == nil {
		t.Fatal("missing count accepted as zero")
	}
}

func TestDailyCounts(t *testing.T) {
	start := testSnapshot("2026-01-02", 10)
	end := testSnapshot("2026-01-03", 15)
	day := testTime("2026-01-02T00:00:00+09:00")
	selected := end.Releases[0]
	if got := dailyCount(start, end, selected, day); got != "5" {
		t.Fatalf("increase: %s", got)
	}
	end.Releases[0].Assets[0].DownloadCount = 10
	if got := dailyCount(start, end, selected, day); got != "0" {
		t.Fatalf("zero: %s", got)
	}
	end.Releases[0].Assets[0].DownloadCount = 9
	if got := dailyCount(start, end, selected, day); !strings.Contains(got, "decreased") {
		t.Fatalf("decrease: %s", got)
	}
	end.Releases[0].Assets[0].DownloadCount = 15
	end.Releases[0].Assets = append(end.Releases[0].Assets, &Asset{
		ID: 2, Name: "neo-server-v1-linux-arm64.zip", DownloadCount: 3, CreatedAt: "2026-01-02T12:00:00+09:00",
	})
	if got := dailyCount(start, end, selected, day); got != "8" {
		t.Fatalf("new asset: %s", got)
	}
	end.Releases[0].Assets[1].CreatedAt = "2026-01-01T12:00:00+09:00"
	if got := dailyCount(start, end, selected, day); !strings.Contains(got, "uncertain") {
		t.Fatalf("uncertain asset: %s", got)
	}
	end.Releases[0].Assets = end.Releases[0].Assets[1:]
	end.Releases[0].Assets[0].CreatedAt = "2026-01-02T12:00:00+09:00"
	if got := dailyCount(start, end, selected, day); !strings.Contains(got, "removed or replaced") {
		t.Fatalf("replacement: %s", got)
	}
	if got := dailyCount(nil, end, selected, day); !strings.Contains(got, "missing") {
		t.Fatalf("missing snapshot: %s", got)
	}
	selected.PublishedAt = "2026-01-03T12:00:00+09:00"
	if got := dailyCount(start, end, selected, day); got != "—" {
		t.Fatalf("not published: %s", got)
	}
}

func TestDailyNewRelease(t *testing.T) {
	start := testSnapshot("2026-01-02", 10)
	end := testSnapshot("2026-01-03", 15)
	end.Releases[0].ID = 2
	end.Releases[0].PublishedAt = "2026-01-02T12:00:00+09:00"
	end.Releases[0].Assets[0].CreatedAt = "2026-01-02T12:01:00+09:00"
	day := testTime("2026-01-02T00:00:00+09:00")
	if got := dailyCount(start, end, end.Releases[0], day); got != "15" {
		t.Fatalf("new release: %s", got)
	}
	end.Releases[0].PublishedAt = "2026-01-01T12:00:00+09:00"
	if got := dailyCount(start, end, end.Releases[0], day); !strings.Contains(got, "release missing") {
		t.Fatalf("old release absent from start: %s", got)
	}
}

func TestDailyLateSnapshot(t *testing.T) {
	start := testSnapshot("2026-01-02", 10)
	end := testSnapshot("2026-01-03", 15)
	day := testTime("2026-01-02T00:00:00+09:00")
	for _, late := range []*snapshot{start, end} {
		original := late.EndedAt
		late.EndedAt = late.StartedAt.Add(24 * time.Minute)
		if got := dailyCount(start, end, end.Releases[0], day); got != "N/A (late snapshot)" {
			t.Fatalf("late boundary: %s", got)
		}
		late.EndedAt = original
	}
	end.EndedAt = testTime("2026-01-03T00:30:00+09:00")
	if got := dailyCount(start, end, end.Releases[0], day); got != "5" {
		t.Fatalf("boundary at 00:30: %s", got)
	}
}

func TestDailyOutputWithLateSnapshot(t *testing.T) {
	dir := t.TempDir()
	start := testSnapshot("2026-01-02", 10)
	end := testSnapshot("2026-01-03", 15)
	end.StartedAt = testTime("2026-01-03T08:19:00+09:00")
	end.EndedAt = testTime("2026-01-03T08:20:00+09:00")
	for _, s := range []*snapshot{start, end} {
		if _, err := storeSnapshot(filepath.Join(dir, "download-stats"), s); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := printDaily(&out, dir, 1, 1, testTime("2026-01-03T08:21:00+09:00")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Warning: latest snapshot completed after 00:30 KST", "2026-01-02", "N/A (late snapshot)", "2026-01-03T08:20:00+09:00"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestDailyOutputAndMissingBoundary(t *testing.T) {
	dir := t.TempDir()
	for _, s := range []*snapshot{testSnapshot("2026-01-01", 10), testSnapshot("2026-01-02", 15), testSnapshot("2026-01-04", 20)} {
		if _, err := storeSnapshot(filepath.Join(dir, "download-stats"), s); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := printDaily(&out, dir, 10, 3, testTime("2026-01-06T00:00:00+09:00")); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"stale", "2026-01-01", "5", "2026-01-02", "2026-01-03", "N/A (missing or invalid snapshot)", "v1"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "2026-01-01" && fields[1] != "5" {
			t.Fatalf("incorrect daily count: %s", line)
		}
	}
}

func TestDailyStaleWarningAfterSchedule(t *testing.T) {
	dir := t.TempDir()
	if _, err := storeSnapshot(filepath.Join(dir, "download-stats"), testSnapshot("2026-01-01", 10)); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		at   string
		want bool
	}{
		{"2026-01-02T00:06:59+09:00", false},
		{"2026-01-02T00:07:00+09:00", true},
	} {
		var out bytes.Buffer
		if err := printDaily(&out, dir, 1, 1, testTime(tt.at)); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "latest snapshot is stale"); got != tt.want {
			t.Fatalf("%s: stale warning = %v, want %v", tt.at, got, tt.want)
		}
	}
}

func TestDailyCLI(t *testing.T) {
	dir := t.TempDir()
	for _, s := range []*snapshot{testSnapshot("2026-01-01", 10), testSnapshot("2026-01-02", 15)} {
		if _, err := storeSnapshot(filepath.Join(dir, "download-stats"), s); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "run", ".", "-daily", "-offline", "-last", "1", "-days", "1", "-data-dir", dir)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "2026-01-01  5") {
		t.Fatalf("daily CLI: %v\n%s", err, out)
	}
	cmd = exec.Command("go", "run", ".", "-daily", "-offline", "-refresh", "-data-dir", dir)
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "-refresh requires -daily and cannot be used with -offline") {
		t.Fatalf("conflicting flags: %v\n%s", err, out)
	}
}

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCollectionDataDir(t *testing.T) {
	oldClient := githubClient
	t.Cleanup(func() { githubClient = oldClient })
	githubClient = &http.Client{Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
		body := `[{"id":1,"tag_name":"v1","published_at":"2025-01-01T00:00:00+09:00","assets":[]}]`
		if strings.HasSuffix(req.URL.Path, "/assets") {
			body = `[{"id":1,"name":"neo-server-v1-linux-amd64.zip","download_count":10,"created_at":"2025-01-01T00:00:00+09:00"}]`
		}

		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
	})}
	dir := t.TempDir()
	now := func() time.Time { return testTime("2026-01-02T00:07:00+09:00") }
	if err := collectSnapshot(dir, now, ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "download-stats", "snapshots", "2026-01-02.json")
	if _, err := loadSnapshot(path); err != nil {
		t.Fatalf("snapshot not stored in data directory: %v", err)
	}
}

func TestCollectionAfterScheduledWindow(t *testing.T) {
	oldClient := githubClient
	t.Cleanup(func() { githubClient = oldClient })
	githubClient = &http.Client{Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`[]`)), Header: make(http.Header), Request: req}, nil
	})}
	dir := t.TempDir()
	now := func() time.Time { return testTime("2026-01-02T08:19:00+09:00") }
	if err := collectSnapshot(dir, now, ""); err != nil {
		t.Fatalf("late collection: %v", err)
	}
	path := filepath.Join(dir, "download-stats", "snapshots", "2026-01-02.json")
	s, err := loadSnapshot(path)
	if err != nil || snapshotInWindow(s) {
		t.Fatalf("late snapshot: %v, %v", s, err)
	}
}

func TestCacheDataDir(t *testing.T) {
	oldClient := githubClient
	t.Cleanup(func() { githubClient = oldClient })
	requests := 0
	githubClient = &http.Client{Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		body := `[{"id":1,"tag_name":"v1","published_at":"2025-01-01T00:00:00+09:00","assets":[]}]`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: req}, nil
	})}
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		releases, cached, err := CountDownloads("test", "repo", dir)
		if err != nil || len(releases) != 1 || cached != (i == 1) {
			t.Fatalf("fetch %d: releases=%v cached=%v err=%v", i, releases, cached, err)
		}
	}
	if requests != 1 {
		t.Fatalf("expected one API request, got %d", requests)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache", "releases.json")); err != nil {
		t.Fatalf("cache not stored in data directory: %v", err)
	}
}

func TestDataDirCLI(t *testing.T) {
	home, custom := t.TempDir(), t.TempDir()
	defaultDir := filepath.Join(home, ".config", "neo-count")
	for _, tc := range []struct {
		dir, tag string
		count    int64
	}{
		{defaultDir, "default-release", 15},
		{custom, "custom-release", 17},
	} {
		releases := testSnapshot("2026-01-02", tc.count).Releases
		releases[0].TagName = tc.tag
		data, err := json.Marshal(releases)
		if err != nil {
			t.Fatal(err)
		}
		cacheDir := filepath.Join(tc.dir, "cache")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cacheDir, "releases.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
		for _, s := range []*snapshot{testSnapshot("2026-01-01", 10), testSnapshot("2026-01-02", tc.count)} {
			s.Releases[0].TagName = tc.tag
			if _, err := storeSnapshot(filepath.Join(tc.dir, "download-stats"), s); err != nil {
				t.Fatal(err)
			}
		}
	}
	goCache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	goModCache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "GOCACHE="+filepath.Join(goCache, "go-build"), "GOMODCACHE="+strings.TrimSpace(string(goModCache)))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"(cached)", "default-release"}},
		{[]string{"--data-dir", custom}, []string{"(cached)", "custom-release"}},
		{[]string{"-daily", "-offline", "-days", "1"}, []string{"default-release", "2026-01-01  5"}},
		{[]string{"-daily", "-offline", "-days", "1", "--data-dir", custom}, []string{"custom-release", "2026-01-01  7"}},
	} {
		out := run(tc.args...)
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Errorf("run %v missing %q:\n%s", tc.args, want, out)
			}
		}
	}
}

func TestFetchReleasesPagesAssetsAndErrors(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing token")
		}

		switch r.URL.Path {
		case "/repos/test/repo/releases":
			fmt.Fprint(w, `[{"id":1,"tag_name":"v1","published_at":"2025-01-01T00:00:00+09:00","assets":[]}]`)
		case "/repos/test/repo/releases/1/assets":
			if r.URL.Query().Get("page") == "2" {
				if fail {
					http.Error(w, "rate limited", http.StatusTooManyRequests)
				} else {
					fmt.Fprint(w, `[{"id":101,"name":"extra","download_count":1}]`)
				}
				return
			}
			items := make([]map[string]any, 100)
			for i := range items {
				items[i] = map[string]any{"id": i + 1, "name": fmt.Sprint(i), "download_count": 1}
			}
			if err := json.NewEncoder(w).Encode(items); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	releases, err := fetchReleases(server.URL, "test", "repo", "test-token", true)
	if err != nil || len(releases) != 1 || len(releases[0].Assets) != 101 {
		t.Fatalf("fetch: releases=%v err=%v", releases, err)
	}
	fail = true
	if releases, err := fetchReleases(server.URL, "test", "repo", "test-token", true); err == nil || releases != nil {
		t.Fatalf("partial fetch returned success: releases=%v err=%v", releases, err)
	}
}

func TestFetchReleasePagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var items []map[string]any
		if r.URL.Query().Get("page") == "1" {
			for i := 1; i <= 100; i++ {
				items = append(items, map[string]any{"id": i, "tag_name": fmt.Sprint(i), "assets": []any{}})
			}
		} else {
			items = []map[string]any{{"id": 101, "tag_name": "101", "assets": []any{}}}
		}
		if err := json.NewEncoder(w).Encode(items); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	releases, err := fetchReleases(server.URL, "test", "repo", "", false)
	if err != nil || len(releases) != 101 {
		t.Fatalf("release pagination: count=%d err=%v", len(releases), err)
	}
}
