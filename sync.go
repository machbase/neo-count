package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

const (
	statsRemote          = "https://github.com/machbase/neo-count.git"
	statsScheduleOffset  = 7 * time.Minute
	statsFinalOffset     = 35 * time.Minute
	statsRefreshInterval = 5 * time.Minute
)

var statsBranch = plumbing.NewBranchReferenceName("download-stats")

type syncState struct {
	Version   int       `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
	Revision  string    `json:"revision"`
}

func needsStatsRefresh(now, checked time.Time, hasExpected bool) bool {
	now, checked = now.UTC(), checked.UTC()
	if checked.IsZero() || checked.After(now) {
		return true
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	scheduled, final := day.Add(statsScheduleOffset), day.Add(statsFinalOffset)
	if now.Before(scheduled) {
		if checked.Before(day.AddDate(0, 0, -1).Add(statsFinalOffset)) {
			return true
		}
		return !hasExpected && now.Sub(checked) >= statsRefreshInterval
	}
	if checked.Before(scheduled) {
		return true
	}
	if now.Before(final) {
		return now.Sub(checked) >= statsRefreshInterval
	}
	if checked.Before(final) {
		return true
	}
	return !hasExpected && now.Sub(checked) >= statsRefreshInterval
}

func refreshStats(dir, remoteURL string, now func() time.Time, force bool) error {
	statePath := filepath.Join(dir, ".download-stats-sync.json")
	data, err := os.ReadFile(statePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read statistics sync state: %w", err)
	}
	var state syncState
	if err == nil {
		decodeErr := json.Unmarshal(data, &state)
		_, hashErr := hex.DecodeString(state.Revision)
		if decodeErr != nil || state.Version != 1 || state.CheckedAt.IsZero() || len(state.Revision) != 40 || hashErr != nil {
			fmt.Fprintln(os.Stderr, "neo-count: invalid statistics sync state; checking the remote")
			state = syncState{}
		}
	}
	path := filepath.Join(dir, "download-stats")
	_, statErr := os.Lstat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("check statistics checkout: %w", statErr)
	}
	if statErr == nil && state.Version == 1 {
		repo, _, head, err := openStatsCheckout(path, remoteURL)
		if err != nil {
			return err
		}
		remoteHead, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", "download-stats"), true)
		if err != nil {
			return fmt.Errorf("read remote statistics branch: %w", err)
		}
		current := now().UTC()
		if state.CheckedAt.After(current) {
			fmt.Fprintln(os.Stderr, "neo-count: statistics sync time is in the future; checking the remote")
		}
		day := time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, time.UTC)
		if current.Before(day.Add(statsScheduleOffset)) {
			day = day.AddDate(0, 0, -1)
		}
		_, snapshotErr := loadSnapshot(filepath.Join(path, "snapshots", day.Format(snapshotTime)+".json"))
		if snapshotErr != nil && !os.IsNotExist(snapshotErr) {
			fmt.Fprintf(os.Stderr, "neo-count: expected snapshot %s is invalid: %v\n", day.Format(snapshotTime), snapshotErr)
		}
		if !force && head.Hash().String() == state.Revision && head.Hash() == remoteHead.Hash() &&
			!needsStatsRefresh(current, state.CheckedAt, snapshotErr == nil) {
			return nil
		}
	}
	if err := syncStats(dir, remoteURL); err != nil {
		return err
	}
	_, _, head, err := openStatsCheckout(path, remoteURL)
	if err != nil {
		return err
	}
	return writeSyncState(statePath, syncState{Version: 1, CheckedAt: now().UTC(), Revision: head.Hash().String()})
}

func writeSyncState(path string, state syncState) (err error) {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode statistics sync state: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".download-stats-sync-*.tmp")
	if err != nil {
		return fmt.Errorf("create statistics sync state: %w", err)
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !os.IsNotExist(removeErr) {
			err = errors.Join(err, fmt.Errorf("remove temporary sync state: %w", removeErr))
		}
	}()
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return fmt.Errorf("write statistics sync state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close statistics sync state: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("save statistics sync state: %w", err)
	}
	return nil
}

func withStatsLock(dir string, fn func() error) (err error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, ".download-stats.lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("lock statistics directory (check for another running process or a stale lock at %s): %w", path, err)
	}
	defer func() {
		if removeErr := os.Remove(path); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove statistics lock: %w", removeErr))
		}
	}()
	if err := lock.Close(); err != nil {
		return fmt.Errorf("close statistics lock: %w", err)
	}
	return fn()
}

func syncStats(dir, remoteURL string) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(dir, "download-stats")
	_, err = os.Lstat(path)
	if os.IsNotExist(err) {
		tmp, createErr := os.MkdirTemp(dir, ".download-stats-clone-")
		if createErr != nil {
			return fmt.Errorf("create temporary checkout: %w", createErr)
		}
		defer func() {
			if tmp != "" {
				if removeErr := os.RemoveAll(tmp); removeErr != nil {
					err = errors.Join(err, fmt.Errorf("remove temporary checkout: %w", removeErr))
				}
			}
		}()
		_, err = git.PlainCloneContext(ctx, tmp, false, &git.CloneOptions{
			URL: remoteURL, ReferenceName: statsBranch, SingleBranch: true, Depth: 1, Tags: git.NoTags,
		})
		if err != nil {
			return fmt.Errorf("clone download-stats (ensure the repository is public and the branch exists): %w", err)
		}
		if _, statErr := os.Lstat(path); statErr == nil {
			return fmt.Errorf("statistics checkout already exists at %s", path)
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("check statistics checkout: %w", statErr)
		}
		if err := os.Rename(tmp, path); err != nil {
			return fmt.Errorf("install statistics checkout: %w", err)
		}
		tmp = ""
		return nil
	}
	if err != nil {
		return fmt.Errorf("check statistics checkout: %w", err)
	}
	repo, worktree, _, err := openStatsCheckout(path, remoteURL)
	if err != nil {
		return err
	}
	err = worktree.PullContext(ctx, &git.PullOptions{
		RemoteName: "origin", RemoteURL: remoteURL, ReferenceName: statsBranch, SingleBranch: true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("update statistics checkout: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("read updated statistics HEAD: %w", err)
	}
	remoteHead, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", "download-stats"), true)
	if err != nil {
		return fmt.Errorf("read remote statistics branch: %w", err)
	}
	if head.Hash() != remoteHead.Hash() {
		return fmt.Errorf("local statistics branch differs from origin; refusing to use local commits")
	}
	return nil
}

func openStatsCheckout(path, remoteURL string) (*git.Repository, *git.Worktree, *plumbing.Reference, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open statistics checkout at %s: %w", path, err)
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open statistics origin: %w", err)
	}
	urls := remote.Config().URLs
	if len(urls) != 1 || !sameStatsRemote(urls[0], remoteURL) {
		return nil, nil, nil, fmt.Errorf("statistics checkout origin does not match the expected repository")
	}
	head, err := repo.Head()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read statistics HEAD: %w", err)
	}
	if head.Name() != statsBranch {
		return nil, nil, nil, fmt.Errorf("statistics checkout must be on the download-stats branch")
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open statistics worktree: %w", err)
	}
	status, err := worktree.Status()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("check statistics worktree: %w", err)
	}
	if !status.IsClean() {
		return nil, nil, nil, fmt.Errorf("statistics checkout has local changes; preserve or remove them before updating")
	}
	return repo, worktree, head, nil
}

func sameStatsRemote(actual, expected string) bool {
	if actual == expected || actual == strings.TrimSuffix(expected, ".git") {
		return true
	}
	if expected != statsRemote {
		return false
	}
	return actual == "git@github.com:machbase/neo-count.git" ||
		actual == "git@github.com:machbase/neo-count" ||
		actual == "ssh://git@github.com/machbase/neo-count.git" ||
		actual == "ssh://git@github.com/machbase/neo-count"
}
