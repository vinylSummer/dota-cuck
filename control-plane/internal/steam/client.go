// Package steam integrates the Steam Web API for friends-list and in-match
// status polling, backing the /api/friends endpoint.
package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// dota2AppID is the Steam app id for Dota 2. A friend whose current game id
// equals it is treated as in a match.
const dota2AppID = "570"

// maxSummariesPerCall is the Steam Web API limit on steamids per
// GetPlayerSummaries request.
const maxSummariesPerCall = 100

const defaultBaseURL = "https://api.steampowered.com"

// Client calls the Steam Web API. Construct it with NewClient.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (used by tests to point at a mock).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// NewClient returns a Client bound to apiKey.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// PlayerSummary is a subset of the GetPlayerSummaries player object.
type PlayerSummary struct {
	SteamID      string
	PersonaName  string
	PersonaState int    // 0 = offline; non-zero = some online state
	GameID       string // current game's app id, empty if not in a game
}

// Friend is a friend with derived live status, as the API exposes it.
type Friend struct {
	SteamID     string
	PersonaName string
	Online      bool
	InMatch     bool // currently in a Dota 2 game
}

// Friends returns steamID's friends with online and in-match status. It fetches
// the friend ids, then their summaries (in batches), and derives status.
func (c *Client) Friends(ctx context.Context, steamID string) ([]Friend, error) {
	ids, err := c.GetFriendList(ctx, steamID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []Friend{}, nil
	}
	players, err := c.GetPlayerSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}

	friends := make([]Friend, 0, len(players))
	for _, p := range players {
		friends = append(friends, Friend{
			SteamID:     p.SteamID,
			PersonaName: p.PersonaName,
			Online:      p.PersonaState != 0,
			InMatch:     p.GameID == dota2AppID,
		})
	}
	sort.Slice(friends, func(i, j int) bool {
		return friends[i].PersonaName < friends[j].PersonaName
	})
	return friends, nil
}

// GetFriendList returns the steam ids of steamID's friends. The friend list
// must be public for the API to return it.
func (c *Client) GetFriendList(ctx context.Context, steamID string) ([]string, error) {
	q := url.Values{}
	q.Set("steamid", steamID)
	q.Set("relationship", "friend")

	var body struct {
		FriendsList struct {
			Friends []struct {
				SteamID string `json:"steamid"`
			} `json:"friends"`
		} `json:"friendslist"`
	}
	if err := c.get(ctx, "/ISteamUser/GetFriendList/v1/", q, &body); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(body.FriendsList.Friends))
	for _, f := range body.FriendsList.Friends {
		ids = append(ids, f.SteamID)
	}
	return ids, nil
}

// GetPlayerSummaries returns summaries for the given steam ids, transparently
// splitting into batches of maxSummariesPerCall.
func (c *Client) GetPlayerSummaries(ctx context.Context, steamIDs []string) ([]PlayerSummary, error) {
	out := make([]PlayerSummary, 0, len(steamIDs))
	for start := 0; start < len(steamIDs); start += maxSummariesPerCall {
		end := start + maxSummariesPerCall
		if end > len(steamIDs) {
			end = len(steamIDs)
		}
		batch, err := c.playerSummariesBatch(ctx, steamIDs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (c *Client) playerSummariesBatch(ctx context.Context, steamIDs []string) ([]PlayerSummary, error) {
	q := url.Values{}
	q.Set("steamids", join(steamIDs))

	var body struct {
		Response struct {
			Players []struct {
				SteamID      string `json:"steamid"`
				PersonaName  string `json:"personaname"`
				PersonaState int    `json:"personastate"`
				GameID       string `json:"gameid"`
			} `json:"players"`
		} `json:"response"`
	}
	if err := c.get(ctx, "/ISteamUser/GetPlayerSummaries/v2/", q, &body); err != nil {
		return nil, err
	}
	out := make([]PlayerSummary, 0, len(body.Response.Players))
	for _, p := range body.Response.Players {
		out = append(out, PlayerSummary{
			SteamID:      p.SteamID,
			PersonaName:  p.PersonaName,
			PersonaState: p.PersonaState,
			GameID:       p.GameID,
		})
	}
	return out, nil
}

// get issues a GET to baseURL+path with the api key added, and decodes a JSON
// 200 response into dst.
func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	q.Set("key", c.apiKey)
	u := c.baseURL + path + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("steam: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("steam: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("steam: %s: unexpected status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("steam: %s: decode response: %w", path, err)
	}
	return nil
}

// join concatenates ids with commas (the steamids parameter format).
func join(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}
