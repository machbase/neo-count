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
	statsOwner     = "machbase"
	statsRepo      = "neo-server"
	snapshotTime   = "2006-01-02"
	snapshotWindow = 30 * time.Minute
)

// statsZone is the fixed KST zone that defines the daily boundary; KST has no DST.
var statsZone = time.FixedZone("KST", 9*60*60)

var errIncompatibleSnapshot = errors.New("incompatible snapshot")

// statsDay returns midnight KST of the KST calendar day containing t.
func statsDay(t time.Time) time.Time {
	t = t.In(statsZone)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, statsZone)
}

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
	boundary := statsDay(s.StartedAt)
	if s.StartedAt.IsZero() || s.EndedAt.Before(s.StartedAt) || !s.EndedAt.Before(boundary.AddDate(0, 0, 1)) {
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

func snapshotInWindow(s *snapshot) bool {
	return !s.EndedAt.After(statsDay(s.StartedAt).Add(snapshotWindow))
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
	if filepath.Base(path) != s.StartedAt.In(statsZone).Format(snapshotTime)+".json" {
		return nil, fmt.Errorf("snapshot date does not match filename")
	}
	return &s, nil
}

func storeSnapshot(dir string, s *snapshot) (bool, error) {
	if err := validateSnapshot(s); err != nil {
		return false, err
	}
	dir = filepath.Join(dir, "snapshots")
	path := filepath.Join(dir, s.StartedAt.In(statsZone).Format(snapshotTime)+".json")
	previous, err := loadSnapshot(path)
	if err == nil {
		currentInWindow, previousInWindow := snapshotInWindow(s), snapshotInWindow(previous)
		if previousInWindow && (!currentInWindow || !s.EndedAt.After(previous.EndedAt)) ||
			!previousInWindow && !currentInWindow && !s.EndedAt.Before(previous.EndedAt) {
			return false, nil
		}
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
		fmt.Printf("Stored snapshot %s (%s to %s)\n", started.In(statsZone).Format(snapshotTime), s.StartedAt.In(statsZone).Format(time.RFC3339), s.EndedAt.In(statsZone).Format(time.RFC3339))
		if !snapshotInWindow(s) {
			fmt.Fprintln(os.Stderr, "Warning: snapshot completed after 00:30 KST; daily counts using it will be unavailable")
		}
	} else {
		fmt.Println("A preferred or identical snapshot already exists")
	}
	return nil
}
