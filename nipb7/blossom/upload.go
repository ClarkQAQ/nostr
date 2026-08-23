package blossom

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"

	"fiatjaf.com/nostr"
)

// UploadFilePath uploads a file to the media server, takes a filepath
func (c *Client) UploadFilePath(ctx context.Context, filePath string) (*BlobDescriptor, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", filePath, err)
	}
	defer file.Close()

	bd, err := c.UploadBlob(ctx, file, mime.TypeByExtension(filepath.Ext(filePath)))
	if err != nil {
		return nil, fmt.Errorf("%w -- at path %s", err, filePath)
	}

	return bd, nil
}

// Upload uploads a file to the media server
func (c *Client) UploadBlob(ctx context.Context, file io.ReadSeeker, contentType string) (*BlobDescriptor, error) {
	sha := sha256.New()
	size, err := io.Copy(sha, file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	hash := sha.Sum(nil)

	_, err = file.Seek(0, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to reset file position: %w", err)
	}

	bd := BlobDescriptor{}
	err = c.httpCall(ctx, "PUT", "upload", contentType, func() string {
		return c.authorizationHeader(ctx, func(evt *nostr.Event) {
			evt.Tags = append(evt.Tags, nostr.Tag{"t", "upload"})
			evt.Tags = append(evt.Tags, nostr.Tag{"x", nostr.HexEncodeToString(hash[:])})
		})
	}, file, size, &bd)
	if err != nil {
		return nil, fmt.Errorf("failed to upload: %w", err)
	}

	return &bd, nil
}
