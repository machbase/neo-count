package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var githubClient = &http.Client{Timeout: 30 * time.Second}

type githubRelease struct {
	ID          int64         `json:"id"`
	Name        string        `json:"name"`
	TagName     string        `json:"tag_name"`
	PublishedAt string        `json:"published_at"`
	Draft       bool          `json:"draft"`
	PreRelease  bool          `json:"prerelease"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DownloadCount *int64 `json:"download_count"`
	Size          int64  `json:"size"`
	ContentType   string `json:"content_type"`
	State         string `json:"state"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	DownloadURL   string `json:"browser_download_url"`
}

func githubPage[T any](endpoint, token string, page int) ([]T, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("per_page", "100")
	q.Set("page", fmt.Sprint(page))
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := githubClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	var data []T
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("decode %s: %w", u, err)
	}
	if data == nil {
		return nil, fmt.Errorf("invalid null response from %s", u)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values from %s", u)
		}
		return nil, fmt.Errorf("decode %s: %w", u, err)
	}
	return data, nil
}

func fetchReleases(base, organization, repository, token string, allAssets bool) ([]*Release, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases", base, url.PathEscape(organization), url.PathEscape(repository))
	releases := make([]*Release, 0)
	for page := 1; ; page++ {
		items, err := githubPage[githubRelease](endpoint, token, page)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if allAssets && item.Draft {
				continue
			}
			if item.ID <= 0 || item.TagName == "" {
				return nil, fmt.Errorf("invalid release in page %d", page)
			}
			rel := &Release{
				ID: item.ID, Name: item.Name, TagName: item.TagName,
				PublishedAt: item.PublishedAt, Draft: item.Draft, PreRelease: item.PreRelease,
				Assets: make([]*Asset, 0),
			}
			assets := item.Assets
			if allAssets {
				assets = nil
				assetURL := fmt.Sprintf("%s/%d/assets", endpoint, item.ID)
				for assetPage := 1; ; assetPage++ {
					batch, err := githubPage[githubAsset](assetURL, token, assetPage)
					if err != nil {
						return nil, err
					}
					assets = append(assets, batch...)
					if len(batch) < 100 {
						break
					}
				}
			}
			for _, a := range assets {
				if a.ID <= 0 || a.Name == "" || a.DownloadCount == nil || *a.DownloadCount < 0 {
					return nil, fmt.Errorf("invalid asset in release %s", item.TagName)
				}
				rel.Assets = append(rel.Assets, &Asset{
					ID: a.ID, Name: a.Name, DownloadCount: *a.DownloadCount, Size: a.Size,
					ContentType: a.ContentType, State: a.State, CreatedAt: a.CreatedAt,
					UpdatedAt: a.UpdatedAt, DownloadUrl: a.DownloadURL,
				})
			}
			releases = append(releases, rel)
		}
		if len(items) < 100 {
			break
		}
	}
	return releases, nil
}
