package blossom

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// mirrorHTTPClient is used to fetch blobs for the /mirror endpoint. The URL
// comes from the request body of a caller that has only proven upload intent,
// not that the destination is safe to reach, so its Transport refuses to
// connect to loopback, private, link-local, unspecified, or multicast
// addresses - including ones a hostname only resolves to later. net.Dialer's
// Control hook fires with the literal address about to be connected to,
// after DNS resolution and immediately before the actual socket connect, so
// this can't be bypassed by whatever the hostname resolves to at fetch time
// (e.g. DNS rebinding).
var mirrorHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(network, address string, c syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}

				ip := net.ParseIP(host)
				if ip == nil {
					return fmt.Errorf("refusing to dial unparseable address %q", address)
				}

				if isBlockedMirrorIP(ip) {
					return fmt.Errorf("refusing to dial local/internal address %q", address)
				}

				return nil
			},
		}).DialContext,
	},
}

// isBlockedMirrorIP reports whether ip is a loopback, private, link-local,
// unspecified, or multicast address - i.e. not something the server should
// fetch on behalf of a /mirror request.
func isBlockedMirrorIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}
