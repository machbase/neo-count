package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ArtifactDownload struct {
	TagVersion    string
	OS            string
	Arch          string
	DownloadCount int64
}

func main() {
	recentLimit := 10
	days := 30
	var daily, collect, offline, refresh bool
	var dataDir string
	flag.IntVar(&recentLimit, "last", recentLimit, "Limit the number of recent releases to display")
	flag.IntVar(&days, "days", days, "Number of completed KST days to display")
	flag.BoolVar(&daily, "daily", false, "Display approximate daily download counts")
	flag.BoolVar(&collect, "collect", false, "Collect a daily snapshot without using the cache")
	flag.BoolVar(&offline, "offline", false, "Read daily snapshots without updating them")
	flag.BoolVar(&refresh, "refresh", false, "Force an update of daily snapshots")
	flag.StringVar(&dataDir, "data-dir", "", "Data directory for the cache and snapshots (default: ~/.config/neo-count)")
	flag.Parse()

	if offline && (!daily || collect) {
		exitError(fmt.Errorf("-offline requires -daily"))
	}
	if refresh && (!daily || collect || offline) {
		exitError(fmt.Errorf("-refresh requires -daily and cannot be used with -offline"))
	}
	if dataDir == "" {
		explicit := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "data-dir" {
				explicit = true
			}
		})
		if explicit {
			exitError(fmt.Errorf("-data-dir must not be empty"))
		}
		home, err := os.UserHomeDir()
		if err != nil {
			exitError(fmt.Errorf("determine home directory: %w", err))
		}
		dataDir = filepath.Join(home, ".config", "neo-count")
	}

	if daily || collect {
		if daily && collect {
			exitError(fmt.Errorf("-daily and -collect cannot be used together"))
		}
		if recentLimit <= 0 || days <= 0 {
			exitError(fmt.Errorf("-last and -days must be positive"))
		}
		var err error
		if collect {
			err = withStatsLock(dataDir, func() error {
				return collectSnapshot(dataDir, time.Now, os.Getenv("GITHUB_TOKEN"))
			})
		} else {
			err = withStatsLock(dataDir, func() error {
				if !offline {
					if err := refreshStats(dataDir, statsRemote, time.Now, refresh); err != nil {
						return err
					}
				}
				return printDaily(os.Stdout, dataDir, recentLimit, days, time.Now())
			})
		}
		if err != nil {
			exitError(err)
		}
		return
	}

	rels, cached, err := CountDownloads("machbase", "neo-server", dataDir)
	if err != nil {
		exitError(err)
	}
	isCached := func() string {
		if cached {
			return " (cached)"
		}
		return ""
	}
	fmt.Printf("Total %d releases%s\n", len(rels), isCached())
	totalDownloadCount := 0
	totalSrcDownloadCount := 0

	nrows := 0
	prevPubTime := time.Now()
	for _, r := range rels {
		subTotal := 0
		srcTotal := 0
		artifacts := make([]ArtifactDownload, 0, 6)
		for _, a := range r.Assets {
			toks := assetTokens(a.Name)
			if len(toks) < 5 {
				// src downloads, not packages
				srcTotal += int(a.DownloadCount)
				totalSrcDownloadCount += int(a.DownloadCount)
				continue
			} else if len(toks) > 5 {
				// rc version has one more dash
				toks = append(toks[:3], toks[4:]...)
				subTotal += int(a.DownloadCount)
				totalDownloadCount += int(a.DownloadCount)
			} else {
				// normal package
				subTotal += int(a.DownloadCount)
				totalDownloadCount += int(a.DownloadCount)
			}
			ad := ArtifactDownload{}
			ad.TagVersion = toks[2]
			ad.OS = toks[3]
			ad.Arch = toks[4]
			ad.DownloadCount = a.DownloadCount
			artifacts = append(artifacts, ad)
		}
		if totalDownloadCount == 0 && totalSrcDownloadCount == 0 {
			continue
		}
		latestDays := 0
		if pubTime, err := time.Parse("2006-01-02T15:04:05Z", r.PublishedAt); err != nil {
			fmt.Println("invalid timeformat: ", r.PublishedAt)
		} else {
			pubSince := prevPubTime.Sub(pubTime)
			prevPubTime = pubTime
			latestDays = int(pubSince.Hours()/24) + 1
			r.PublishedAt = pubTime.In(time.Local).Format("2006-01-02")
		}
		nrows++
		if nrows > recentLimit {
			continue
		}

		fields := make([]string, 0, len(artifacts))
		for _, a := range artifacts {
			fields = append(fields, fmt.Sprintf("%s-%s: %2d", a.OS, a.Arch, a.DownloadCount))
		}
		// download per day
		dpd := "   0"
		if latestDays > 0 {
			dpd = fmt.Sprintf("%4.1f", (float64(subTotal) / float64(latestDays)))
		}
		fmt.Println(fmt.Sprintf("%-12s", r.TagName), r.PublishedAt, fmt.Sprintf("%4d", subTotal), dpd, strings.Join(fields, "  "))
	}
	fmt.Println("Total Downloads:", totalDownloadCount+totalSrcDownloadCount, "package:", totalDownloadCount, "src.zip:", totalSrcDownloadCount)
}

func assetTokens(name string) []string {
	return strings.Split(strings.TrimSuffix(name, ".zip"), "-")
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, "neo-count:", err)
	os.Exit(1)
}

func CountDownloads(organization string, repository string, dataDir string) ([]*Release, bool, error) {
	path := filepath.Join(dataDir, "cache", "releases.json")
	if stat, err := os.Stat(path); err == nil && time.Since(stat.ModTime()) < time.Hour {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false, fmt.Errorf("read cache: %w", err)
		}
		ret := make([]*Release, 0)
		if err := json.Unmarshal(data, &ret); err == nil {
			return ret, true, nil
		}
		fmt.Fprintln(os.Stderr, "neo-count: invalid cache; fetching releases again")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("stat cache: %w", err)
	}

	releases, err := fetchReleases("https://api.github.com", organization, repository, "", false)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, false, fmt.Errorf("create cache directory: %w", err)
	}
	data, err := json.MarshalIndent(releases, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, false, fmt.Errorf("write cache: %w", err)
	}

	return releases, false, nil
}

type Release struct {
	ID          int64
	Name        string
	TagName     string
	PublishedAt string
	Draft       bool
	PreRelease  bool
	Assets      []*Asset
}

type Asset struct {
	ID            int64
	Name          string
	DownloadCount int64
	Size          int64
	ContentType   string
	State         string
	CreatedAt     string
	UpdatedAt     string
	DownloadUrl   string
}
