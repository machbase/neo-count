package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

func releaseByID(s *snapshot, id int64) *Release {
	for _, r := range s.Releases {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func dailyCount(start, end *snapshot, selected *Release, day time.Time) string {
	published, err := time.Parse(time.RFC3339, selected.PublishedAt)
	if err != nil {
		return "N/A (invalid publication time)"
	}
	if !published.Before(day.AddDate(0, 0, 1)) {
		return "—"
	}
	if start == nil || end == nil {
		return "N/A (missing or invalid snapshot)"
	}
	after := releaseByID(end, selected.ID)
	if after == nil {
		return "N/A (release missing)"
	}
	before := releaseByID(start, selected.ID)
	old := make(map[int64]*Asset)
	if before == nil {
		if !published.After(start.EndedAt) || published.After(end.EndedAt) {
			return "N/A (release missing)"
		}
	} else {
		for _, a := range before.Assets {
			old[a.ID] = a
		}
	}
	var total int64
	for _, a := range after.Assets {
		previous, found := old[a.ID]
		if found {
			delete(old, a.ID)
			if a.DownloadCount < previous.DownloadCount {
				return "N/A (count decreased)"
			}
			if (len(assetTokens(a.Name)) >= 5) != (len(assetTokens(previous.Name)) >= 5) {
				return "N/A (asset classification changed)"
			}
			if len(assetTokens(a.Name)) >= 5 {
				total += a.DownloadCount - previous.DownloadCount
			}
		} else {
			created, err := time.Parse(time.RFC3339, a.CreatedAt)
			if err != nil || !created.After(start.EndedAt) || created.After(end.EndedAt) {
				return "N/A (asset creation uncertain)"
			}
			if len(assetTokens(a.Name)) >= 5 {
				total += a.DownloadCount
			}
		}
	}
	if len(old) != 0 {
		return "N/A (asset removed or replaced)"
	}
	return fmt.Sprint(total)
}

func printDaily(w io.Writer, dir string, limit, days int, now time.Time) error {
	if limit <= 0 || days <= 0 {
		return fmt.Errorf("-last and -days must be positive")
	}
	path := filepath.Join(dir, "download-stats", "snapshots")
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read snapshots in %s: %w", path, err)
	}
	var dates []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		date := strings.TrimSuffix(name, ".json")
		if _, err := time.Parse(snapshotTime, date); err == nil {
			dates = append(dates, date)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	var latest *snapshot
	for _, date := range dates {
		s, err := loadSnapshot(filepath.Join(path, date+".json"))
		if errors.Is(err, errIncompatibleSnapshot) {
			return fmt.Errorf("%s: %w", date, err)
		}
		if err != nil {
			fmt.Fprintf(w, "Warning: invalid snapshot %s: %v\n", date, err)
			continue
		}
		latest = s
		break
	}
	if latest == nil {
		return fmt.Errorf("no valid snapshots in %s", path)
	}
	boundary := time.Date(latest.StartedAt.UTC().Year(), latest.StartedAt.UTC().Month(), latest.StartedAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
	fmt.Fprintf(w, "Approximate daily package downloads (UTC); latest snapshot %s–%s\n", latest.StartedAt.UTC().Format(time.RFC3339), latest.EndedAt.UTC().Format(time.RFC3339))
	if !now.UTC().Before(boundary.AddDate(0, 0, 1).Add(statsScheduleOffset)) {
		fmt.Fprintf(w, "Warning: latest snapshot is stale (boundary %s UTC)\n", boundary.Format(snapshotTime))
	}
	selected := make([]*Release, 0, limit)
	for _, r := range latest.Releases {
		if len(selected) == limit {
			break
		}
		selected = append(selected, r)
	}
	cache := map[string]*snapshot{boundary.Format(snapshotTime): latest}
	load := func(date time.Time) (*snapshot, error) {
		key := date.Format(snapshotTime)
		if s, ok := cache[key]; ok {
			return s, nil
		}
		s, err := loadSnapshot(filepath.Join(path, key+".json"))
		if errors.Is(err, errIncompatibleSnapshot) {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if err != nil {
			fmt.Fprintf(w, "Warning: snapshot %s unavailable: %v\n", key, err)
		}
		cache[key] = s
		return s, nil
	}
	type row struct {
		day        time.Time
		start, end *snapshot
	}
	rows := make([]row, 0, days)
	for i := days; i >= 1; i-- {
		day := boundary.AddDate(0, 0, -i)
		start, err := load(day)
		if err != nil {
			return err
		}
		end, err := load(day.AddDate(0, 0, 1))
		if err != nil {
			return err
		}
		rows = append(rows, row{day, start, end})
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprint(table, "Date (UTC)")
	for _, r := range selected {
		fmt.Fprintf(table, "\t%s", r.TagName)
	}
	fmt.Fprintln(table, "\tObservation interval (UTC)")
	for _, row := range rows {
		fmt.Fprint(table, row.day.Format(snapshotTime))
		for _, r := range selected {
			fmt.Fprintf(table, "\t%s", dailyCount(row.start, row.end, r, row.day))
		}
		interval := "unavailable"
		if row.start != nil && row.end != nil {
			interval = row.start.EndedAt.UTC().Format(time.RFC3339) + " to " + row.end.EndedAt.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(table, "\t%s\n", interval)
	}
	return table.Flush()
}
