// Package github provides a GitHub API client for listing repositories
// owned by the authenticated user.
package github

import (
	"context"
	"fmt"

	gh "github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"

	"github.com/Yeti47/github-mirror/internal/config"
	"github.com/Yeti47/github-mirror/internal/git"
)

// Client wraps the GitHub Repositories API.
type Client struct {
	repos *gh.RepositoriesService
}

// NewClient creates a new authenticated GitHub API client using the provided PAT.
func NewClient(pat config.PersonalAccessToken) *Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: pat.Value()})
	tc := oauth2.NewClient(context.Background(), ts)
	ghc := gh.NewClient(tc)
	return &Client{repos: ghc.Repositories}
}

// ListOwnedRepos returns all repositories owned by the authenticated user,
// including private and archived repositories. Disabled repositories are excluded.
// Results are paginated internally; the caller receives the complete list.
func (c *Client) ListOwnedRepos(ctx context.Context) ([]git.Repo, error) {
	opts := &gh.RepositoryListByAuthenticatedUserOptions{
		Affiliation: "owner",
		Visibility:  "all",
		ListOptions: gh.ListOptions{PerPage: 100},
	}

	var result []git.Repo
	for {
		page, resp, err := c.repos.ListByAuthenticatedUser(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("list repos page %d: %w", opts.Page, err)
		}

		for _, r := range page {
			if r.GetDisabled() {
				continue
			}
			result = append(result, git.Repo{
				FullName: r.GetFullName(),
				CloneURL: r.GetCloneURL(),
				Archived: r.GetArchived(),
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return result, nil
}
