package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const defaultGitHubAPIBase = "https://api.github.com/repos/entireio/cli"
const githubAPIBaseEnv = "ENTIRE_UPGRADE_GITHUB_API_BASE_URL"

type ReleaseChecker struct {
	BaseURL string
	Client  *http.Client
}

func (c ReleaseChecker) Latest(ctx context.Context, channel Channel) (Version, error) {
	switch channel {
	case StableChannel:
		return c.latestStable(ctx)
	case NightlyChannel:
		return c.latestNightly(ctx)
	default:
		return Version{}, fmt.Errorf("unsupported release channel %q", channel)
	}
}

func (c ReleaseChecker) latestStable(ctx context.Context) (Version, error) {
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := c.fetchJSON(ctx, "/releases/latest", &release); err != nil {
		return Version{}, err
	}
	if release.TagName == "" {
		return Version{}, fmt.Errorf("latest stable release did not include a tag name")
	}
	return ParseVersion(release.TagName)
}

func (c ReleaseChecker) latestNightly(ctx context.Context) (Version, error) {
	var releases []struct {
		TagName string `json:"tag_name"`
	}
	if err := c.fetchJSON(ctx, "/releases?per_page=100", &releases); err != nil {
		return Version{}, err
	}

	var latest Version
	for _, release := range releases {
		if !strings.Contains(release.TagName, "nightly") {
			continue
		}
		version, err := ParseVersion(release.TagName)
		if err != nil {
			continue
		}
		if !latest.Present || version.Compare(latest) > 0 {
			latest = version
		}
	}
	if !latest.Present {
		return Version{}, fmt.Errorf("no nightly release found")
	}
	return latest, nil
}

func (c ReleaseChecker) fetchJSON(ctx context.Context, path string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = os.Getenv(githubAPIBaseEnv)
	}
	if base == "" {
		base = defaultGitHubAPIBase
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch Entire CLI releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch Entire CLI releases: GitHub returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode Entire CLI release response: %w", err)
	}
	return nil
}
