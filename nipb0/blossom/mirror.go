package blossom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"fiatjaf.com/nostr"
)

// MirrorBlob mirrors a blob from a remote URL to the media server
func (c *Client) MirrorBlob(ctx context.Context, remoteBlobURL string) (*BlobDescriptor, error) {
	bodyBytes, err := json.Marshal(struct{ URL string }{URL: remoteBlobURL})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mirror request: %w", err)
	}

	spl := strings.Split(remoteBlobURL, "/")
	hhash := strings.Split(spl[len(spl)-1], ".")[0]

	bd := BlobDescriptor{}
	err = c.httpCall(ctx, "PUT", "mirror", "application/json", func() string {
		return c.authorizationHeader(ctx, func(evt *nostr.Event) {
			evt.Tags = append(evt.Tags, nostr.Tag{"t", "upload"})
			evt.Tags = append(evt.Tags, nostr.Tag{"x", hhash})
		})
	}, bytes.NewReader(bodyBytes), int64(len(bodyBytes)), &bd)
	if err != nil {
		return nil, fmt.Errorf("failed to mirror blob: %w", err)
	}

	return &bd, nil
}
