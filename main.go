package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type release struct {
	Organization string
	Repository   string
}

type ArtifactDownload struct {
	TagVersion    string
	OS            string
	Arch          string
	DownloadCount int64
}

func main() {
	recentLimit := 10
	flag.IntVar(&recentLimit, "last", recentLimit, "Limit the number of recent releases to display")
	flag.Parse()

	rels, cached, err := CountDownloads("machbase", "neo-server")
	if err != nil {
		panic(err.Error())
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
			toks := strings.Split(strings.TrimSuffix(a.Name, ".zip"), "-")
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

func CountDownloads(organization string, repository string) ([]*Release, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	path := filepath.Join(home, ".config", "neo-download-count")
	if stat, err := os.Stat(path); err == nil && time.Since(stat.ModTime()) < time.Hour {
		fd, _ := os.Open(path)
		defer fd.Close()
		dec := json.NewDecoder(fd)
		ret := make([]*Release, 0)
		if err := dec.Decode(&ret); err == nil {
			return ret, true, nil
		}
	}

	releases := make([]*Release, 0)
	pg := 1
	for {
		path := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=100&page=%d", organization, repository, pg)

	redirected:
		req, err := http.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if err != nil {
			return nil, false, err
		}

		rsp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, false, err
		}
		defer rsp.Body.Close()

		if rsp.StatusCode == http.StatusMovedPermanently || rsp.StatusCode == http.StatusTemporaryRedirect {
			loc, err := rsp.Location()
			if err != nil {
				return nil, false, err
			}
			path = loc.String()
			goto redirected
		}
		if rsp.StatusCode != http.StatusOK {
			return nil, false, err
		}

		buff, err := io.ReadAll(rsp.Body)
		if err != nil {
			return nil, false, err
		}
		payload := string(buff)

		rs := gjson.Get(payload, "@this").Array()
		if len(rs) == 0 {
			break
		} else {
			pg += 1
		}

		for _, r := range rs {
			rel := Release{}
			rel.TagName = r.Get("tag_name").Str
			rel.PublishedAt = r.Get("published_at").Str
			rel.Name = r.Get("name").Str
			rel.Draft = r.Get("draft").Bool()
			rel.PreRelease = r.Get("prerelease").Bool()
			rel.Assets = make([]*Asset, 0)
			releases = append(releases, &rel)

			assets := r.Get("assets").Array()
			for _, entry := range assets {
				asset := Asset{}
				asset.Name = entry.Get("name").Str
				asset.DownloadCount = entry.Get("download_count").Int()
				asset.Size = entry.Get("size").Int()
				asset.ContentType = entry.Get("content_type").Str
				asset.State = entry.Get("state").Str
				asset.CreatedAt = entry.Get("created_at").Str
				asset.UpdatedAt = entry.Get("updated_at").Str
				asset.DownloadUrl = entry.Get("browser_download_url").Str
				rel.Assets = append(rel.Assets, &asset)
			}
		}
	}

	fd, _ := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	defer fd.Close()
	enc := json.NewEncoder(fd)
	enc.SetIndent("", "  ")
	enc.Encode(releases)

	return releases, false, nil
}

type Release struct {
	Name        string
	TagName     string
	PublishedAt string
	Draft       bool
	PreRelease  bool
	Assets      []*Asset
}

type Asset struct {
	Name          string
	DownloadCount int64
	Size          int64
	ContentType   string
	State         string
	CreatedAt     string
	UpdatedAt     string
	DownloadUrl   string
}
