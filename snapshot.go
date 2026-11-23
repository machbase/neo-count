package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	statsOwner   = "machbase"
	statsRepo    = "neo-server"
	snapshotTime = "2006-01-02"
)

var errIncompatibleSnapshot = errors.New("incompatible snapshot")

type snapshot struct {
	Version   int        `json:"version"`
	Owner     string     `json:"owner"`
	Repo      string     `json:"repository"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   time.Time  `json:"ended_at"`
	Releases  []*Release `json:"releases"`
}

func validateSnapshot(s *snapshot) error {
	if s.Version != 1 || s.Owner != statsOwner || s.Repo != statsRepo {
		return fmt.Errorf("%w: version %d, repository %s/%s", errIncompatibleSnapshot, s.Version, s.Owner, s.Repo)
	}
	start, end := s.StartedAt.UTC(), s.EndedAt.UTC()
	boundary := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	if s.StartedAt.IsZero() || end.Before(start) || end.After(boundary.Add(30*time.Minute)) {
		return fmt.Errorf("invalid collection time")
	}
	if s.Releases == nil {
		return fmt.Errorf("missing releases")
	}
	releaseIDs, assetIDs := map[int64]bool{}, map[int64]bool{}
	for _, r := range s.Releases {
		if r == nil || r.ID <= 0 || r.TagName == "" || releaseIDs[r.ID] || r.Assets == nil {
			return fmt.Errorf("invalid or duplicate release")
		}
		if _, err := time.Parse(time.RFC3339, r.PublishedAt); err != nil {
			return fmt.Errorf("invalid publication time for %s", r.TagName)
		}
		releaseIDs[r.ID] = true
		for _, a := range r.Assets {
			if a == nil || a.ID <= 0 || a.Name == "" || a.DownloadCount < 0 || assetIDs[a.ID] {
				return fmt.Errorf("invalid or duplicate asset in %s", r.TagName)
			}
			assetIDs[a.ID] = true
		}
	}
	return nil
}

func loadSnapshot(path string) (*snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	var fields struct {
		Releases []struct {
			Assets []struct {
				DownloadCount *int64 `json:"DownloadCount"`
			} `json:"Assets"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode snapshot fields: %w", err)
	}
	for _, r := range fields.Releases {
		for _, a := range r.Assets {
			if a.DownloadCount == nil {
				return nil, fmt.Errorf("snapshot asset is missing download count")
			}
		}
	}
	if err := validateSnapshot(&s); err != nil {
		return nil, err
	}
	if filepath.Base(path) != s.StartedAt.UTC().Format(snapshotTime)+".json" {
		return nil, fmt.Errorf("snapshot date does not match filename")
	}
	return &s, nil
}

func storeSnapshot(dir string, s *snapshot) (bool, error) {
	if err := validateSnapshot(s); err != nil {
		return false, err
	}
	dir = filepath.Join(dir, "snapshots")
	path := filepath.Join(dir, s.StartedAt.UTC().Format(snapshotTime)+".json")
	previous, err := loadSnapshot(path)
	if err == nil && !s.EndedAt.After(previous.EndedAt) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read existing snapshot %s: %w", path, err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, err
	}
	file, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return false, err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}

func collectSnapshot(dir string, now func() time.Time, token string) error {
	started := now().UTC()
	boundary := time.Date(started.Year(), started.Month(), started.Day(), 0, 0, 0, 0, time.UTC)
	if started.After(boundary.Add(30 * time.Minute)) {
		return fmt.Errorf("collection must start and finish between 00:00 and 00:30 UTC")
	}
	releases, err := fetchReleases("https://api.github.com", statsOwner, statsRepo, token, true)
	if err != nil {
		return err
	}
	s := &snapshot{Version: 1, Owner: statsOwner, Repo: statsRepo, StartedAt: started, EndedAt: now().UTC(), Releases: releases}
	changed, err := storeSnapshot(filepath.Join(dir, "download-stats"), s)
	if err != nil {
		return fmt.Errorf("store snapshot: %w", err)
	}
	if changed {
		fmt.Printf("Stored snapshot %s (%s to %s)\n", started.Format(snapshotTime), s.StartedAt.Format(time.RFC3339), s.EndedAt.Format(time.RFC3339))
	} else {
		fmt.Println("A newer or identical snapshot already exists")
	}
	return nil
}
