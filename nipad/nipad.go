package nipad

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"fiatjaf.com/nostr"
	"github.com/mailru/easyjson"
)

//easyjson:json
type WellKnownResponse map[string]Path

//easyjson:json
type Path struct {
	Filter nostr.Filter `json:"filter"`
	Relays []string     `json:"relays,omitempty"`
}

func ParseURL(rawurl string) (domain string, path string, err error) {
	if !strings.Contains(rawurl, "://") {
		rawurl = "https://" + rawurl
	}

	u, err := url.Parse(rawurl)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse '%s': %w", rawurl, err)
	}

	if u.Host == "" {
		return "", "", fmt.Errorf("no domain in '%s'", rawurl)
	}

	if u.Path == "" {
		u.Path = "/"
	}

	return u.Host, u.Path, nil
}

func Resolve(ctx context.Context, rawurl string) (*Path, error) {
	result, path, err := Fetch(ctx, rawurl)
	if err != nil {
		return nil, err
	}

	entry, ok := result[path]
	if !ok {
		return nil, fmt.Errorf("no entry for path '%s'", path)
	}

	return &entry, nil
}

var httpClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func Fetch(ctx context.Context, rawurl string) (resp WellKnownResponse, path string, err error) {
	domain, path, err := ParseURL(rawurl)
	if err != nil {
		return resp, path, fmt.Errorf("failed to parse '%s': %w", rawurl, err)
	}

	scheme := "https"
	if u, err := url.Parse(rawurl); err == nil && u.Scheme != "" {
		scheme = u.Scheme
	}

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s://%s/.well-known/nostr.json?path=%s", scheme, domain, url.QueryEscape(path)), nil)
	if err != nil {
		return resp, path, fmt.Errorf("failed to create a request: %w", err)
	}

	res, err := httpClient.Do(req)
	if err != nil {
		return resp, path, fmt.Errorf("request failed: %w", err)
	}
	defer res.Body.Close()

	var result WellKnownResponse
	if err := easyjson.UnmarshalFromReader(res.Body, &result); err != nil {
		return resp, path, fmt.Errorf("failed to decode json response: %w", err)
	}

	return result, path, nil
}
