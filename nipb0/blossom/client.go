package blossom

import (
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/valyala/fasthttp"
)

// Client represents a Blossom client for interacting with a media server
type Client struct {
	mediaserver string
	httpClient  *fasthttp.Client
	signer      nostr.Signer
}

// NewClient creates a new Blossom client
func NewClient(mediaserver string, signer nostr.Signer) *Client {
	mediaserver = "http" + nostr.NormalizeURL(mediaserver)[2:]

	return &Client{
		mediaserver: strings.TrimSuffix(mediaserver, "/") + "/",
		httpClient:  createHTTPClient(),
		signer:      signer,
	}
}

// createHTTPClient creates a properly configured HTTP client
func createHTTPClient() *fasthttp.Client {
	return &fasthttp.Client{
		MaxIdleConnDuration:           time.Hour,
		DisableHeaderNamesNormalizing: true, // because our headers are properly constructed
		DisablePathNormalizing:        true,

		Name: "nl-b", // user-agent

		// increase DNS cache time to an hour instead of default minute
		Dial: (&fasthttp.TCPDialer{
			Concurrency:      4096,
			DNSCacheDuration: time.Hour,
		}).Dial,
	}
}

// GetSigner returns the client's signer
func (c *Client) GetSigner() nostr.Signer {
	return c.signer
}

// GetMediaServer returns the client's media server URL
func (c *Client) GetMediaServer() string {
	return c.mediaserver
}
