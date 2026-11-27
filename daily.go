package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	if !snapshotInWindow(start) || !snapshotInWindow(end) {
		return "N/A (late snapshot)"
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

func formatDailyValue(value string) string {
	if strings.HasPrefix(value, "N/A (") {
		return "N/A"
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}
	digits := strconv.FormatInt(count, 10)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
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
	boundary := statsDay(latest.StartedAt)
	fmt.Fprintf(w, "Approximate daily package downloads (KST)\nLatest snapshot: %s to %s KST\n",
		latest.StartedAt.In(statsZone).Format("2006-01-02 15:04"),
		latest.EndedAt.In(statsZone).Format("2006-01-02 15:04"))
	if !snapshotInWindow(latest) {
		fmt.Fprintln(w, "Warning: latest snapshot completed after 00:30 KST")
	}
	if !now.Before(boundary.AddDate(0, 0, 1).Add(statsScheduleOffset)) {
		fmt.Fprintf(w, "Warning: latest snapshot is stale (boundary %s KST)\n", boundary.Format(snapshotTime))
	}
	selected := make([]*Release, 0, limit)
	for _, r := range latest.Releases {
		if len(selected) == limit {
			break
		}
		selected = append(selected, r)
	}
	cache := map[string]*snapshot{boundary.Format(snapshotTime): latest}
	missing := make(map[string]bool)
	var warnings []string
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
			if errors.Is(err, os.ErrNotExist) {
				missing[key] = true
			} else {
				warnings = append(warnings, fmt.Sprintf("Warning: invalid snapshot %s: %v", key, err))
			}
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
	for _, warning := range warnings {
		fmt.Fprintln(w, warning)
	}
	if len(missing) > 0 {
		dates := make([]string, 0, len(missing))
		for date := range missing {
			dates = append(dates, date)
		}
		sort.Strings(dates)
		fmt.Fprintf(w, "Warning: %d required snapshots missing (%s through %s)\n",
			len(dates), dates[0], dates[len(dates)-1])
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprint(table, "Date (KST)")
	for _, r := range selected {
		fmt.Fprintf(table, "\t%s", r.TagName)
	}
	fmt.Fprintln(table, "\tSnapshot window (KST)")
	reasons := make(map[string]bool)
	for _, row := range rows {
		fmt.Fprint(table, row.day.Format(snapshotTime))
		for _, r := range selected {
			value := dailyCount(row.start, row.end, r, row.day)
			if strings.HasPrefix(value, "N/A (") {
				reasons[strings.TrimSuffix(strings.TrimPrefix(value, "N/A ("), ")")] = true
			}
			fmt.Fprintf(table, "\t%s", formatDailyValue(value))
		}
		interval := "unavailable"
		if row.start != nil && row.end != nil {
			interval = row.start.EndedAt.In(statsZone).Format("01-02 15:04") + " to " +
				row.end.EndedAt.In(statsZone).Format("01-02 15:04")
		}
		fmt.Fprintf(table, "\t%s\n", interval)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if len(reasons) > 0 {
		ordered := make([]string, 0, len(reasons))
		for reason := range reasons {
			ordered = append(ordered, reason)
		}
		sort.Strings(ordered)
		fmt.Fprintf(w, "N/A: %s\n", strings.Join(ordered, "; "))
	}
	fmt.Fprintln(w, "— = release not yet published; 0 = no observed increase.")
	return nil
}
