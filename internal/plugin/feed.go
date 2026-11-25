package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/HengWh/woodwind-music/internal/local"
)

const maxFeedBytes = 4 << 20

var (
	sha256Hex  = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	feedClient = &http.Client{Timeout: 30 * time.Second}
)

// Feed is a JSON plugin catalog subscribed by URL.
type Feed struct {
	URL         string      `json:"url,omitempty"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Homepage    string      `json:"homepage,omitempty"`
	UpdatedAt   string      `json:"updatedAt,omitempty"`
	Plugins     []FeedEntry `json:"plugins"`
}

// FeedEntry describes one installable plugin listed in a Feed.
type FeedEntry struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Version            string   `json:"version,omitempty"`
	Description        string   `json:"description,omitempty"`
	Repo               string   `json:"repo,omitempty"`
	Commit             string   `json:"commit,omitempty"`
	DownloadURL        string   `json:"downloadUrl,omitempty"`
	SHA256             string   `json:"sha256,omitempty"`
	Homepage           string   `json:"homepage,omitempty"`
	License            string   `json:"license,omitempty"`
	Platforms          []string `json:"platforms,omitempty"`
	PureJS             *bool    `json:"pureJs,omitempty"`
	MinWoodwindVersion string   `json:"minWoodwindVersion,omitempty"`
	Verification       string   `json:"verification,omitempty"`
}

// DesktopOnly reports whether the entry explicitly declares that it cannot run
// on Android.
func (e FeedEntry) DesktopOnly() bool {
	if e.PureJS != nil && !*e.PureJS {
		return true
	}
	if len(e.Platforms) == 0 {
		return false
	}
	for _, p := range e.Platforms {
		if strings.EqualFold(strings.TrimSpace(p), "android") {
			return false
		}
	}
	return true
}

// IsPureJS reports whether the entry explicitly declares pure JS / Android compatibility.
func (e FeedEntry) IsPureJS() bool {
	if e.PureJS != nil {
		return *e.PureJS
	}
	for _, p := range e.Platforms {
		if strings.EqualFold(strings.TrimSpace(p), "android") {
			return true
		}
	}
	return false
}

// ValidateFeedURL checks that a feed or archive URL uses HTTPS (or loopback HTTP
// for local development and tests).
func ValidateFeedURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("plugin feed URL is required")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid URL: %s", s)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.String(), nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return u.String(), nil
		}
		return "", errors.New("URL must use https://")
	default:
		return "", errors.New("URL must use https://")
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// FetchFeed downloads and validates a JSON plugin feed from rawURL.
func FetchFeed(rawURL string) (*Feed, error) {
	cleanURL, err := ValidateFeedURL(rawURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", cleanURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "woodwind-music")
	req.Header.Set("Accept", "application/json")
	res, err := feedClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugin feed answered %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, errors.New("plugin feed is too large")
	}
	var feed Feed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("invalid plugin feed JSON: %w", err)
	}
	feed.URL = cleanURL
	feed.Name = strings.TrimSpace(feed.Name)
	if feed.Name == "" {
		if u, err := url.Parse(cleanURL); err == nil && u.Host != "" {
			feed.Name = u.Host
		} else {
			feed.Name = cleanURL
		}
	}
	seen := map[string]bool{}
	valid := make([]FeedEntry, 0, len(feed.Plugins))
	for _, entry := range feed.Plugins {
		normalized, err := normalizeFeedEntry(entry)
		if err != nil {
			return nil, err
		}
		if seen[normalized.ID] {
			continue
		}
		seen[normalized.ID] = true
		valid = append(valid, normalized)
	}
	feed.Plugins = valid
	return &feed, nil
}

func normalizeFeedEntry(e FeedEntry) (FeedEntry, error) {
	e.ID = strings.TrimSpace(e.ID)
	e.Name = strings.TrimSpace(e.Name)
	e.Version = strings.TrimSpace(e.Version)
	e.Repo = strings.TrimSpace(e.Repo)
	e.Commit = strings.TrimSpace(e.Commit)
	e.DownloadURL = strings.TrimSpace(e.DownloadURL)
	e.SHA256 = strings.ToLower(strings.TrimSpace(e.SHA256))
	e.Homepage = strings.TrimSpace(e.Homepage)
	e.License = strings.TrimSpace(e.License)
	e.Description = strings.TrimSpace(e.Description)
	e.Verification = strings.TrimSpace(e.Verification)
	e.MinWoodwindVersion = strings.TrimSpace(e.MinWoodwindVersion)
	if e.ID == "" || e.ID == local.ID || !pluginName.MatchString(e.ID) {
		return FeedEntry{}, fmt.Errorf("invalid plugin id in feed: %q", e.ID)
	}
	if e.Name == "" {
		e.Name = e.ID
	}
	if e.Repo != "" {
		repo, err := ParseRepo(e.Repo)
		if err != nil {
			return FeedEntry{}, fmt.Errorf("plugin %s: %w", e.ID, err)
		}
		e.Repo = repo
	}
	if e.Commit != "" && !commitSHA.MatchString(e.Commit) {
		return FeedEntry{}, fmt.Errorf("plugin %s: invalid commit %q", e.ID, e.Commit)
	}
	if e.DownloadURL != "" {
		dl, err := ValidateFeedURL(e.DownloadURL)
		if err != nil {
			return FeedEntry{}, fmt.Errorf("plugin %s: %w", e.ID, err)
		}
		e.DownloadURL = dl
		if e.Version == "" {
			return FeedEntry{}, fmt.Errorf("plugin %s: downloadUrl requires version", e.ID)
		}
		if !sha256Hex.MatchString(e.SHA256) {
			return FeedEntry{}, fmt.Errorf("plugin %s: downloadUrl requires a 64-character hex sha256", e.ID)
		}
	} else if e.Repo == "" {
		return FeedEntry{}, fmt.Errorf("plugin %s: requires repo or downloadUrl", e.ID)
	}
	if e.Homepage != "" {
		if hp, err := ValidateFeedURL(e.Homepage); err == nil {
			e.Homepage = hp
		} else {
			e.Homepage = ""
		}
	}
	if len(e.Platforms) > 0 {
		e.Platforms = slices.Clone(e.Platforms)
	}
	return e, nil
}

// ReleaseFromFeedEntry turns a feed entry into a Release that can be downloaded
// directly without querying GitHub's API when DownloadURL or Commit is given.
func ReleaseFromFeedEntry(feedURL string, entry FeedEntry) (*Release, error) {
	e, err := normalizeFeedEntry(entry)
	if err != nil {
		return nil, err
	}
	if e.DownloadURL != "" {
		return &Release{
			Repo:        e.Repo,
			Version:     e.Version,
			FeedURL:     feedURL,
			DownloadURL: e.DownloadURL,
			SHA256:      e.SHA256,
			url:         e.DownloadURL,
		}, nil
	}
	if e.Commit != "" {
		ver := e.Version
		if ver == "" {
			ver = e.Commit[:min(len(e.Commit), 12)]
		}
		return &Release{
			Repo:    e.Repo,
			Version: ver,
			FeedURL: feedURL,
			url:     githubAPI + "/repos/" + e.Repo + "/zipball/" + e.Commit,
		}, nil
	}
	rel, err := Latest(e.Repo)
	if err != nil {
		return nil, err
	}
	rel.FeedURL = feedURL
	return rel, nil
}

// FetchFeedEntry downloads and verifies a plugin described by a feed entry.
func FetchFeedEntry(feedURL string, entry FeedEntry, dir string) (*Pending, error) {
	e, err := normalizeFeedEntry(entry)
	if err != nil {
		return nil, err
	}
	rel, err := ReleaseFromFeedEntry(feedURL, e)
	if err != nil {
		return nil, err
	}
	p, err := FetchRelease(rel, dir)
	if err != nil {
		return nil, err
	}
	if p.Manifest.ID != e.ID {
		p.Discard()
		return nil, fmt.Errorf("%s: expected plugin %s, got %s", e.ID, e.ID, p.Manifest.ID)
	}
	if e.Commit != "" && e.Version != "" && p.Manifest.Version != "" && p.Manifest.Version != e.Version {
		p.Discard()
		return nil, fmt.Errorf("%s: expected version %s, got %s", e.ID, e.Version, p.Manifest.Version)
	}
	return p, nil
}
