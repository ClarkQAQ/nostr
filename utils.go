package nostr

import (
	"bytes"
	"cmp"
	"net"
	"net/http"
	"net/url"
	"unsafe"

	"github.com/templexxx/xhex"
)

// IsValidRelayURL checks if a URL is a valid relay URL (ws:// or wss://).
func IsValidRelayURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Scheme != "wss" && parsed.Scheme != "ws" {
		return false
	}
	return true
}

// HexEncodeToString encodes src into a hex string.
func HexEncodeToString(src []byte) string {
	dst := make([]byte, len(src)*2)
	xhex.Encode(dst, src)
	return unsafe.String(unsafe.SliceData(dst), len(dst))
}

// HexDecodeString decodes a hex string into bytes.
func HexDecodeString(s string) ([]byte, error) {
	src := unsafe.Slice(unsafe.StringData(s), len(s))
	if len(src)%2 != 0 {
		return nil, xhex.ErrLength
	}
	dst := make([]byte, len(src)/2)
	err := xhex.Decode(dst, src)
	if err != nil {
		return nil, err
	}
	return dst, nil
}

// IsValid32ByteHex checks if a string is a valid 32-byte hex string.
func IsValid32ByteHex(thing string) bool {
	if !isLowerHex(thing) {
		return false
	}
	if len(thing) != 64 {
		return false
	}
	_, err := HexDecodeString(thing)
	return err == nil
}

// CompareEvent is meant to to be used with slices.Sort
func CompareEvent(a, b Event) int {
	if a.CreatedAt == b.CreatedAt {
		return bytes.Compare(a.ID[:], b.ID[:])
	}
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// CompareEventReverse is meant to to be used with slices.Sort
func CompareEventReverse(b, a Event) int {
	if a.CreatedAt == b.CreatedAt {
		return bytes.Compare(a.ID[:], b.ID[:])
	}
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// CompareRelayEvent is meant to to be used with slices.Sort
func CompareRelayEvent(a, b RelayEvent) int {
	if a.CreatedAt == b.CreatedAt {
		return bytes.Compare(a.ID[:], b.ID[:])
	}
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// CompareRelayEventReverse is meant to to be used with slices.Sort
func CompareRelayEventReverse(b, a RelayEvent) int {
	if a.CreatedAt == b.CreatedAt {
		return bytes.Compare(a.ID[:], b.ID[:])
	}
	return cmp.Compare(a.CreatedAt, b.CreatedAt)
}

// AppendUnique adds items to an array only if they don't already exist in the array.
// Returns the modified array.
func AppendUnique[I comparable](list []I, newEls ...I) []I {
ex:
	for _, newEl := range newEls {
		for _, el := range list {
			if el == newEl {
				continue ex
			}
		}
		list = append(list, newEl)
	}
	return list
}

func IsOlder(previous, next Event) bool {
	return previous.CreatedAt < next.CreatedAt ||
		(previous.CreatedAt == next.CreatedAt && bytes.Compare(previous.ID[:], next.ID[:]) == 1)
}

var DefaultIPChecker = NewIPChecker(nil)

func HTTPHostURL(r *http.Request, checker *IPChecker, paths ...string) *url.URL {
	u := baseURL(r, checker)
	u.Path, u.RawQuery = "", ""

	if len(paths) > 0 {
		u = u.JoinPath(paths...)
	}

	return u
}

func HTTPURL(r *http.Request, checker *IPChecker, paths ...string) *url.URL {
	u := baseURL(r, checker)

	if len(paths) > 0 {
		u = u.JoinPath(paths...)
	}

	return u
}

func baseURL(r *http.Request, checker *IPChecker) *url.URL {
	u := &url.URL{
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}

	if r.URL.Scheme != "" && r.URL.Host != "" {
		u.Scheme = r.URL.Scheme
		u.Host = r.URL.Host

		return u
	}

	u.Scheme = "http"
	u.Host = r.Host

	if r.TLS != nil {
		u.Scheme = "https"
	}

	if checker == nil {
		return u
	}

	remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
	if remoteHost == "" {
		remoteHost = r.RemoteAddr
	}

	if ip := net.ParseIP(remoteHost); ip == nil || !checker.Trust(ip) {
		return u
	}

	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		u.Host = fh
	}
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		u.Scheme = fp
	}
	if fport := r.Header.Get("X-Forwarded-Port"); fport != "" {
		host, _, _ := net.SplitHostPort(u.Host)
		if host == "" {
			host = u.Host
		}

		if (u.Scheme == "https" && fport != "443") ||
			(u.Scheme == "http" && fport != "80") {
			u.Host = net.JoinHostPort(host, fport)
		} else {
			u.Host = host
		}
	}

	return u
}

// copy from https://github.com/labstack/echo/v4@v4.14.0/ip.go
type IPChecker struct {
	trustExtraRanges []*net.IPNet
	trustLoopback    bool
	trustLinkLocal   bool
	trustPrivateNet  bool
}

// TrustOption is config for which IP address to trust
type TrustOption func(*IPChecker)

// TrustLoopback configures if you trust loopback address (default: true).
func TrustLoopback(v bool) TrustOption {
	return func(c *IPChecker) {
		c.trustLoopback = v
	}
}

// TrustLinkLocal configures if you trust link-local address (default: true).
func TrustLinkLocal(v bool) TrustOption {
	return func(c *IPChecker) {
		c.trustLinkLocal = v
	}
}

// TrustPrivateNet configures if you trust private network address (default: true).
func TrustPrivateNet(v bool) TrustOption {
	return func(c *IPChecker) {
		c.trustPrivateNet = v
	}
}

// TrustIPRange add trustable IP ranges using CIDR notation.
func TrustIPRange(ipRange *net.IPNet) TrustOption {
	return func(c *IPChecker) {
		c.trustExtraRanges = append(c.trustExtraRanges, ipRange)
	}
}

func NewIPChecker(configs []TrustOption) *IPChecker {
	checker := &IPChecker{trustLoopback: true, trustLinkLocal: true, trustPrivateNet: true}
	for _, configure := range configs {
		configure(checker)
	}
	return checker
}

func (c *IPChecker) Trust(ip net.IP) bool {
	if c.trustLoopback && ip.IsLoopback() {
		return true
	}
	if c.trustLinkLocal && ip.IsLinkLocalUnicast() {
		return true
	}
	if c.trustPrivateNet && ip.IsPrivate() {
		return true
	}
	for _, trustedRange := range c.trustExtraRanges {
		if trustedRange.Contains(ip) {
			return true
		}
	}
	return false
}
