package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func testStatsRepo(t *testing.T) (string, *git.Repository) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, statsBranch)); err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

func testStatsCommit(t *testing.T, dir string, repo *git.Repository, s *snapshot) {
	t.Helper()
	if _, err := storeSnapshot(dir, s); err != nil {
		t.Fatal(err)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join("snapshots", s.StartedAt.In(statsZone).Format(snapshotTime)+".json")
	if _, err := worktree.Add(name); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("test snapshot", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@example.invalid", When: time.Unix(1, 0)},
	}); err != nil {
		t.Fatal(err)
	}
}

func syncForTest(dir, remote string) error {
	return withStatsLock(dir, func() error { return syncStats(dir, remote) })
}

func TestSyncStatsCloneAndUpdate(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	dir := t.TempDir()
	if err := syncForTest(dir, remoteDir); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := syncForTest(dir, remoteDir); err != nil {
		t.Fatalf("already up to date: %v", err)
	}
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-02", 15))
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-03", 18))
	if err := syncForTest(dir, remoteDir); err != nil {
		t.Fatalf("update: %v", err)
	}
	var out bytes.Buffer
	if err := printDaily(&out, dir, 10, 2, testTime("2026-01-03T00:10:00+09:00")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-01-01  5", "2026-01-02  3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out.String())
		}
	}
}

func TestSyncStatsProtectsLocalChanges(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	dir := t.TempDir()
	if err := syncForTest(dir, remoteDir); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(dir, "download-stats")
	path := filepath.Join(checkout, "snapshots", "2026-01-01.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("local change"), 0644); err != nil {
		t.Fatal(err)
	}
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-02", 15))
	if err := syncForTest(dir, remoteDir); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("dirty checkout accepted: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "local change" {
		t.Fatalf("local change overwritten: %q, %v", got, err)
	}
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(checkout, "user-file")
	if err := os.WriteFile(untracked, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syncForTest(dir, remoteDir); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("untracked file accepted: %v", err)
	}
	if got, err := os.ReadFile(untracked); err != nil || string(got) != "keep" {
		t.Fatalf("untracked file changed: %q, %v", got, err)
	}
}

func TestSyncStatsRejectsForeignOriginAndLocalCommits(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	dir := t.TempDir()
	if err := syncForTest(dir, remoteDir); err != nil {
		t.Fatal(err)
	}
	if err := syncForTest(dir, t.TempDir()); err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("foreign origin accepted: %v", err)
	}
	checkout := filepath.Join(dir, "download-stats")
	local, err := git.PlainOpen(checkout)
	if err != nil {
		t.Fatal(err)
	}
	testStatsCommit(t, checkout, local, testSnapshot("2026-01-02", 15))
	before, err := local.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := syncForTest(dir, remoteDir); err == nil {
		t.Fatalf("local commit accepted: %v", err)
	}
	after, err := local.Head()
	if err != nil || after.Hash() != before.Hash() {
		t.Fatalf("local commit was changed: %v", err)
	}
}

func TestSyncStatsProtectsIgnoredFiles(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	dir := t.TempDir()
	if err := syncForTest(dir, remoteDir); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(dir, "download-stats")
	exclude := filepath.Join(checkout, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("snapshots/2026-01-02.json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(checkout, "snapshots", "2026-01-02.json")
	if err := os.WriteFile(path, []byte("local ignored file"), 0644); err != nil {
		t.Fatal(err)
	}
	local, err := git.PlainOpen(checkout)
	if err != nil {
		t.Fatal(err)
	}
	before, err := local.Head()
	if err != nil {
		t.Fatal(err)
	}
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-02", 15))
	if err := syncForTest(dir, remoteDir); err == nil {
		t.Fatal("ignored file collision accepted")
	}
	after, err := local.Head()
	if err != nil || after.Hash() != before.Hash() {
		t.Fatalf("failed update changed HEAD: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "local ignored file" {
		t.Fatalf("ignored file overwritten: %q, %v", got, err)
	}
}

func TestSyncStatsFailedCloneAndLock(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	dir := t.TempDir()
	if err := syncForTest(dir, remoteDir); err == nil {
		t.Fatal("empty remote unexpectedly cloned")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed clone left files: %v, %v", entries, err)
	}
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	if err := syncForTest(dir, remoteDir); err != nil {
		t.Fatalf("retry: %v", err)
	}
	err = withStatsLock(dir, func() error {
		return withStatsLock(dir, func() error { return nil })
	})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("concurrent lock accepted: %v", err)
	}
	if err := withStatsLock(dir, func() error { return nil }); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestNeedsStatsRefresh(t *testing.T) {
	tests := []struct {
		name        string
		now         string
		checked     string
		hasExpected bool
		want        bool
	}{
		{"before schedule", "2026-01-02T00:06:59+09:00", "2026-01-01T00:35:00+09:00", true, false},
		{"previous check not final", "2026-01-02T00:06:59+09:00", "2026-01-01T00:34:59+09:00", true, true},
		{"schedule reached", "2026-01-02T00:07:00+09:00", "2026-01-02T00:06:59+09:00", true, true},
		{"within retry interval", "2026-01-02T00:11:59+09:00", "2026-01-02T00:07:00+09:00", true, false},
		{"retry interval reached", "2026-01-02T00:12:00+09:00", "2026-01-02T00:07:00+09:00", true, true},
		{"final check", "2026-01-02T00:35:00+09:00", "2026-01-02T00:34:59+09:00", true, true},
		{"after final check", "2026-01-02T23:59:59+09:00", "2026-01-02T00:35:00+09:00", true, false},
		{"missing snapshot retry wait", "2026-01-02T00:39:59+09:00", "2026-01-02T00:35:00+09:00", false, false},
		{"missing snapshot retry", "2026-01-02T00:40:00+09:00", "2026-01-02T00:35:00+09:00", false, true},
		{"future check", "2026-01-02T00:35:00+09:00", "2026-01-02T00:36:00+09:00", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := needsStatsRefresh(testTime(tt.now), testTime(tt.checked), tt.hasExpected)
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func refreshForTest(dir, remote, at string) error {
	return withStatsLock(dir, func() error {
		return refreshStats(dir, remote, func() time.Time { return testTime(at) }, false)
	})
}

func TestRefreshStatsScheduleAndFailure(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	dir := t.TempDir()
	if err := refreshForTest(dir, remoteDir, "2026-01-01T00:36:00+09:00"); err != nil {
		t.Fatalf("initial clone: %v", err)
	}
	statePath := filepath.Join(dir, ".download-stats-sync.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(remoteDir, ".git")
	offlinePath := filepath.Join(remoteDir, ".git-offline")
	if err := os.Rename(gitPath, offlinePath); err != nil {
		t.Fatal(err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:06:59+09:00"); err != nil {
		t.Fatalf("before schedule should use local data: %v", err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:07:00+09:00"); err == nil {
		t.Fatal("unavailable remote should fail after schedule")
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed fetch changed sync state: %v", err)
	}
	if err := os.Rename(offlinePath, gitPath); err != nil {
		t.Fatal(err)
	}
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-02", 15))
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:07:00+09:00"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	path := filepath.Join(dir, "download-stats", "snapshots", "2026-01-02.json")
	if s, err := loadSnapshot(path); err != nil || s.Releases[0].Assets[0].DownloadCount != 15 {
		t.Fatalf("new snapshot not fetched: %v", err)
	}
	later := testSnapshot("2026-01-02", 18)
	later.StartedAt = later.StartedAt.Add(time.Minute)
	later.EndedAt = later.EndedAt.Add(time.Minute)
	testStatsCommit(t, remoteDir, remote, later)
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:11:59+09:00"); err != nil {
		t.Fatal(err)
	}
	if s, err := loadSnapshot(path); err != nil || s.Releases[0].Assets[0].DownloadCount != 15 {
		t.Fatalf("fetched before retry interval: %v", err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:12:00+09:00"); err != nil {
		t.Fatal(err)
	}
	if s, err := loadSnapshot(path); err != nil || s.Releases[0].Assets[0].DownloadCount != 18 {
		t.Fatalf("updated snapshot not fetched: %v", err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:34:59+09:00"); err != nil {
		t.Fatal(err)
	}
	final := testSnapshot("2026-01-02", 20)
	final.StartedAt = final.StartedAt.Add(22 * time.Minute)
	final.EndedAt = final.EndedAt.Add(22 * time.Minute)
	testStatsCommit(t, remoteDir, remote, final)
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:35:00+09:00"); err != nil {
		t.Fatal(err)
	}
	if s, err := loadSnapshot(path); err != nil || s.Releases[0].Assets[0].DownloadCount != 20 {
		t.Fatalf("final snapshot not fetched: %v", err)
	}
	if err := os.Rename(gitPath, offlinePath); err != nil {
		t.Fatal(err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T23:59:59+09:00"); err != nil {
		t.Fatalf("after final check should use local data: %v", err)
	}
	if err := withStatsLock(dir, func() error {
		return refreshStats(dir, remoteDir, func() time.Time { return testTime("2026-01-02T23:59:59+09:00") }, true)
	}); err == nil {
		t.Fatal("forced refresh should contact the unavailable remote")
	}
}

func TestRefreshStatsMissingAndDirtyCheckout(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	dir := t.TempDir()
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:36:00+09:00"); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(remoteDir, ".git")
	offlinePath := filepath.Join(remoteDir, ".git-offline")
	if err := os.Rename(gitPath, offlinePath); err != nil {
		t.Fatal(err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:40:59+09:00"); err != nil {
		t.Fatalf("should wait before retry: %v", err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:41:00+09:00"); err == nil {
		t.Fatal("missing snapshot should trigger a retry")
	}
	if err := os.Rename(offlinePath, gitPath); err != nil {
		t.Fatal(err)
	}
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-02", 15))
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:41:00+09:00"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "download-stats", "snapshots", "2026-01-02.json")
	if err := os.WriteFile(path, []byte("local change"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:42:00+09:00"); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("dirty cached checkout accepted: %v", err)
	}
}

func TestRefreshStatsInvalidState(t *testing.T) {
	remoteDir, remote := testStatsRepo(t)
	testStatsCommit(t, remoteDir, remote, testSnapshot("2026-01-01", 10))
	dir := t.TempDir()
	if err := refreshForTest(dir, remoteDir, "2026-01-01T00:36:00+09:00"); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, ".download-stats-sync.json")
	if err := os.WriteFile(statePath, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(remoteDir, ".git")
	offlinePath := filepath.Join(remoteDir, ".git-offline")
	if err := os.Rename(gitPath, offlinePath); err != nil {
		t.Fatal(err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:05:00+09:00"); err == nil {
		t.Fatal("invalid state should force a remote check")
	}
	if err := os.Rename(offlinePath, gitPath); err != nil {
		t.Fatal(err)
	}
	if err := refreshForTest(dir, remoteDir, "2026-01-02T00:05:00+09:00"); err != nil {
		t.Fatalf("invalid state recovery: %v", err)
	}
	state, err := os.ReadFile(statePath)
	if err != nil || !json.Valid(state) {
		t.Fatalf("sync state not repaired: %v", err)
	}
}
