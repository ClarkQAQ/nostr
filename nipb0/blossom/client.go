package blossom

import (
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
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
	d := fasthttpproxy.Dialer{
		Timeout:        10 * time.Second,
		ConnectTimeout: 10 * time.Second,
		TCPDialer: fasthttp.TCPDialer{
			// increase DNS cache time to an hour instead of default minute
			Concurrency:      4096,
			DNSCacheDuration: time.Hour,
		},
		DialDualStack: true,
	}
	dialFunc, _ := d.GetDialFunc(true)

	return &fasthttp.Client{
		MaxIdleConnDuration:           time.Hour,
		DisableHeaderNamesNormalizing: true, // because our headers are properly constructed
		DisablePathNormalizing:        true,

		Name: "nl-b", // user-agent

		Dial: dialFunc,
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
