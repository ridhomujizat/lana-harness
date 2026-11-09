package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Channel names (API-UPD-001).
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// Defaults and environment overrides of the release client (API-UPD-001).
const (
	DefaultAPI  = "https://api.github.com"
	DefaultRepo = "ridhomujizat/lana-harness"

	EnvAPI   = "LANA_UPDATE_API"
	EnvRepo  = "LANA_UPDATE_REPO"
	EnvToken = "GITHUB_TOKEN"

	// SumsAsset is the checksum file every release carries.
	SumsAsset = "SHA256SUMS"

	// CheckTimeout is the HTTP limit of one release check (API-UPD-004).
	CheckTimeout = 15 * time.Second

	// releasesBodyLimit caps the releases JSON the client reads.
	releasesBodyLimit = 16 << 20
)

// ValidChannel reports whether name is "stable" or "beta".
func ValidChannel(name string) bool {
	return name == ChannelStable || name == ChannelBeta
}

// AssetName is the release asset for a platform: lana-<GOOS>-<GOARCH>.
func AssetName(goos, goarch string) string {
	return "lana-" + goos + "-" + goarch
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Release is one GitHub release as the update service sees it.
type Release struct {
	Version     Version
	Tag         string
	Name        string
	Notes       string
	URL         string
	PublishedAt time.Time
	Prerelease  bool
	Draft       bool
	Assets      []Asset
}

// Asset returns the asset with the given name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return Asset{}, false
}

// UpgradeNotes reports whether the notes have an "Upgrade notes" heading
// (API-UPD-002 upgrade_notes).
func (r Release) UpgradeNotes() bool { return HasUpgradeNotes(r.Notes) }

var upgradeNotesRE = regexp.MustCompile(`(?mi)^\s{0,3}#{1,6}\s*upgrade notes\b`)

// HasUpgradeNotes reports whether markdown contains a heading "Upgrade notes".
func HasUpgradeNotes(markdown string) bool {
	return upgradeNotesRE.MatchString(markdown)
}

// Client reads releases from the GitHub REST API.
type Client struct {
	HTTP  *http.Client
	API   string
	Repo  string
	Token string
	// UserAgent is sent with every request; GitHub requires one.
	UserAgent string
}

// NewClientFromEnv builds a client from LANA_UPDATE_API, LANA_UPDATE_REPO, and
// GITHUB_TOKEN (API-UPD-001). getenv nil means os.Getenv. A nil httpClient
// gets one with the 15-second check timeout.
func NewClientFromEnv(httpClient *http.Client, getenv func(string) string, userAgent string) *Client {
	if getenv == nil {
		getenv = os.Getenv
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: CheckTimeout}
	}
	api := strings.TrimRight(strings.TrimSpace(getenv(EnvAPI)), "/")
	if api == "" {
		api = DefaultAPI
	}
	repo := strings.Trim(strings.TrimSpace(getenv(EnvRepo)), "/")
	if repo == "" {
		repo = DefaultRepo
	}
	if userAgent == "" {
		userAgent = "lana-selfupdate"
	}
	return &Client{HTTP: httpClient, API: api, Repo: repo, Token: strings.TrimSpace(getenv(EnvToken)), UserAgent: userAgent}
}

// githubRelease is the subset of the GitHub release JSON the client reads.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// Releases lists up to 30 releases: GET {api}/repos/{repo}/releases?per_page=30.
// A tag that is not a semantic version is dropped.
func (c *Client) Releases(ctx context.Context) ([]Release, error) {
	url := c.API + "/repos/" + c.Repo + "/releases?per_page=30"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	// The repository is public; the token only raises the rate limit.
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: CheckTimeout}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		message := strings.TrimSpace(string(snippet))
		var apiErr struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(snippet, &apiErr) == nil && apiErr.Message != "" {
			message = apiErr.Message
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("list releases: GitHub returned %d: %s", resp.StatusCode, message)
	}
	var raw []githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, releasesBodyLimit)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("list releases: decode: %w", err)
	}
	out := make([]Release, 0, len(raw))
	for _, item := range raw {
		version, ok := ParseVersion(item.TagName)
		if !ok {
			continue
		}
		release := Release{
			Version:     version,
			Tag:         item.TagName,
			Name:        item.Name,
			Notes:       item.Body,
			URL:         item.HTMLURL,
			PublishedAt: item.PublishedAt,
			Prerelease:  item.Prerelease,
			Draft:       item.Draft,
		}
		for _, asset := range item.Assets {
			release.Assets = append(release.Assets, Asset{Name: asset.Name, URL: asset.BrowserDownloadURL, Size: asset.Size})
		}
		out = append(out, release)
	}
	return out, nil
}

// Select picks the highest release newer than current on the channel that has
// both the platform asset and SHA256SUMS (REQ-UPD-001). Drafts are ignored;
// "stable" also ignores prereleases, "beta" offers every release. It returns
// false when nothing qualifies.
func Select(releases []Release, current Version, channel, asset string) (Release, bool) {
	var best Release
	found := false
	for _, release := range releases {
		if release.Draft {
			continue
		}
		if channel != ChannelBeta && release.Prerelease {
			continue
		}
		if Compare(release.Version, current) <= 0 {
			continue
		}
		if _, ok := release.Asset(asset); !ok {
			continue
		}
		if _, ok := release.Asset(SumsAsset); !ok {
			continue
		}
		if !found || Compare(release.Version, best.Version) > 0 {
			best, found = release, true
		}
	}
	return best, found
}
