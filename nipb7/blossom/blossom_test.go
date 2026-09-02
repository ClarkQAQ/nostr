package blossom_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	blossomclient "fiatjaf.com/nostr/nipb7/blossom"
	relay_blossom "fiatjaf.com/nostr/relay/blossom"
)

func TestBlossomBasicOperations(t *testing.T) {
	// setup two test servers
	server1 := setupTestServer()
	defer server1.Close()

	server2 := setupTestServer()
	defer server2.Close()

	// create signers for two different pubkeys
	secretKey1 := nostr.Generate()
	secretKey2 := nostr.Generate()
	signer1 := keyer.NewPlainKeySigner(secretKey1)
	signer2 := keyer.NewPlainKeySigner(secretKey2)

	server1Url, e := url.Parse(server1.URL)
	if e != nil {
		t.Fatal(e)
	}
	server2Url, e := url.Parse(server2.URL)
	if e != nil {
		t.Fatal(e)
	}

	// create clients
	client1 := blossomclient.NewClient(server1Url, signer1)
	client2 := blossomclient.NewClient(server1Url, signer2)
	client2Server := blossomclient.NewClient(server2Url, signer2)

	ctx := context.Background()

	// test content
	testContent := []byte("Hello, Blossom World!")
	contentType := "text/plain"

	// 1. Upload blob from first pubkey
	t.Run("UploadBlob", func(t *testing.T) {
		reader := bytes.NewReader(testContent)
		bd, err := client1.UploadBlob(ctx, reader, contentType)
		if err != nil {
			t.Fatalf("Failed to upload blob: %v", err)
		}

		if contentSize := int64(len(testContent)); bd.Size != contentSize {
			t.Errorf("Expected size %d, got %d", contentSize, bd.Size)
		}

		// content type may include charset, just check that it starts with our expected type
		if !strings.HasPrefix(bd.Type, contentType) {
			t.Errorf("Expected type to start with %s, got %s", contentType, bd.Type)
		}

		// verify SHA256 is calculated correctly
		if len(bd.SHA256) != 64 {
			t.Errorf("Expected SHA256 to be 64 characters, got %d", len(bd.SHA256))
		}
	})

	// 2. Download the blob
	t.Run("DownloadBlob", func(t *testing.T) {
		// first get the list to find the hash
		blobs, err := client1.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs: %v", err)
		}

		if len(blobs) != 1 {
			t.Fatalf("Expected 1 blob, got %d", len(blobs))
		}

		hash := blobs[0].SHA256
		downloaded, err := client1.Download(ctx, hash)
		if err != nil {
			t.Fatalf("Failed to download blob: %v", err)
		}

		if !bytes.Equal(downloaded, testContent) {
			t.Errorf("Downloaded content mismatch. Expected %q, got %q", testContent, downloaded)
		}
	})

	// 3. Upload same blob from different pubkey
	t.Run("UploadSameBlobDifferentPubkey", func(t *testing.T) {
		// get the hash from the first upload
		blobs, err := client1.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs: %v", err)
		}

		if len(blobs) != 1 {
			t.Fatalf("Expected 1 blob, got %d", len(blobs))
		}

		hash := blobs[0].SHA256
		reader := bytes.NewReader(testContent)
		bd, err := client2.UploadBlob(ctx, reader, contentType)
		if err != nil {
			t.Fatalf("Failed to upload same blob with different pubkey: %v", err)
		}

		if bd.SHA256 != hash {
			t.Errorf("Hash mismatch for same content. Expected %s, got %s", hash, bd.SHA256)
		}
	})

	// 4. Check list for both pubkeys
	t.Run("ListBlobs", func(t *testing.T) {
		// list from first pubkey
		blobs1, err := client1.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs from pubkey1: %v", err)
		}

		// list from second pubkey
		blobs2, err := client2.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs from pubkey2: %v", err)
		}

		// both should see the same blob
		if len(blobs1) != 1 || len(blobs2) != 1 {
			t.Errorf("Expected both pubkeys to see 1 blob, got %d and %d", len(blobs1), len(blobs2))
		}

		if blobs1[0].SHA256 != blobs2[0].SHA256 {
			t.Errorf("Hash mismatch between pubkey lists: %s vs %s", blobs1[0].SHA256, blobs2[0].SHA256)
		}
	})

	// 5. Delete from first pubkey
	t.Run("DeleteFromFirstPubkey", func(t *testing.T) {
		// get the hash
		blobs, err := client1.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs: %v", err)
		}

		hash := blobs[0].SHA256

		// delete from first pubkey
		err = client1.Delete(ctx, hash)
		if err != nil {
			t.Fatalf("Failed to delete blob from pubkey1: %v", err)
		}

		// first pubkey should see no blobs
		blobs1, err := client1.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs after delete: %v", err)
		}

		// second pubkey should still see the blob
		blobs2, err := client2.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs from pubkey2: %v", err)
		}

		if len(blobs1) != 0 {
			t.Errorf("Expected pubkey1 to see 0 blobs after delete, got %d", len(blobs1))
		}

		if len(blobs2) != 1 {
			t.Errorf("Expected pubkey2 to still see 1 blob after pubkey1 delete, got %d", len(blobs2))
		}

		// download should still work
		downloaded, err := client2.Download(ctx, hash)
		if err != nil {
			t.Fatalf("Failed to download blob after pubkey1 delete: %v", err)
		}

		if !bytes.Equal(downloaded, testContent) {
			t.Errorf("Downloaded content mismatch after delete. Expected %q, got %q", testContent, downloaded)
		}
	})

	// 6. Upload different blobs to second server
	t.Run("UploadToSecondServer", func(t *testing.T) {
		testContent2 := []byte("Hello from server 2!")
		reader := bytes.NewReader(testContent2)
		bd, err := client2Server.UploadBlob(ctx, reader, "text/plain")
		if err != nil {
			t.Fatalf("Failed to upload blob to server2: %v", err)
		}

		// verify it appears in list
		blobs, err := client2Server.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs from server2: %v", err)
		}

		if len(blobs) != 1 {
			t.Errorf("Expected 1 blob on server2, got %d", len(blobs))
		}

		// verify download
		downloaded, err := client2Server.Download(ctx, bd.SHA256)
		if err != nil {
			t.Fatalf("Failed to download from server2: %v", err)
		}

		if !bytes.Equal(downloaded, testContent2) {
			t.Errorf("Downloaded content mismatch from server2")
		}
	})

	// 7. Mirror from server1 to server2
	t.Run("MirrorBetweenServers", func(t *testing.T) {
		// get a blob from server1 (should still be there from pubkey2)
		blobs1, err := client2.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs from server1: %v", err)
		}

		if len(blobs1) == 0 {
			t.Skip("No blobs to mirror")
		}

		sourceURL := server1.URL + "/" + blobs1[0].SHA256

		// mirror to server2
		bd, err := client2Server.MirrorBlob(ctx, sourceURL)
		if err != nil {
			t.Fatalf("Failed to mirror blob: %v", err)
		}

		// verify mirrored blob appears in server2 list
		blobs2, err := client2Server.List(ctx)
		if err != nil {
			t.Fatalf("Failed to list blobs from server2 after mirror: %v", err)
		}

		// should now have 2 blobs (original + mirrored)
		if len(blobs2) < 2 {
			t.Errorf("Expected at least 2 blobs on server2 after mirror, got %d", len(blobs2))
		}

		// verify the mirrored blob can be downloaded
		downloaded, err := client2Server.Download(ctx, bd.SHA256)
		if err != nil {
			t.Fatalf("Failed to download mirrored blob: %v", err)
		}

		// should match the original content
		originalDownloaded, err := client2.Download(ctx, blobs1[0].SHA256)
		if err != nil {
			t.Fatalf("Failed to download original blob for comparison: %v", err)
		}

		if !bytes.Equal(downloaded, originalDownloaded) {
			t.Errorf("Mirrored blob content mismatch")
		}
	})

	// 8. Final list verification
	t.Run("FinalListVerification", func(t *testing.T) {
		// check final state of both servers
		blobs1, err := client2.List(ctx) // server1 with pubkey2
		if err != nil {
			t.Fatalf("Failed final list from server1: %v", err)
		}

		blobs2, err := client2Server.List(ctx) // server2 with pubkey2
		if err != nil {
			t.Fatalf("Failed final list from server2: %v", err)
		}

		// server1 should have the original blob
		if len(blobs1) == 0 {
			t.Errorf("Expected server1 to have blobs, got 0")
		}

		// server2 should have at least the original blob + mirrored blob
		if len(blobs2) < 2 {
			t.Errorf("Expected server2 to have at least 2 blobs, got %d", len(blobs2))
		}

		t.Logf("Final state - Server1: %d blobs, Server2: %d blobs", len(blobs1), len(blobs2))
	})
}

func setupTestServer() *httptest.Server {
	// use memory-based blob index for testing
	memoryIndex := relay_blossom.NewMemoryBlobIndex()

	// setup blob storage in memory
	blobStorage := make(map[string][]byte)

	bl := relay_blossom.New()
	bl.Store = memoryIndex

	bl.StoreBlob = func(ctx context.Context, sha256 string, mimeType string, size int64, reader io.Reader) error {
		blobStorage[sha256], _ = io.ReadAll(reader)
		return nil
	}

	bl.LoadBlob = func(ctx context.Context, sha256 string, mimeType string) (io.ReadSeekCloser, *url.URL, error) {
		if data, ok := blobStorage[sha256]; ok {
			return blossomclient.NopSeekCloser(bytes.NewReader(data)), nil, nil
		}
		return nil, nil, nil
	}

	bl.DeleteBlob = func(ctx context.Context, sha256 string, ext string) error {
		delete(blobStorage, sha256)
		return nil
	}

	server := httptest.NewUnstartedServer(bl)
	server.Start()

	// wait a moment for server to be ready
	time.Sleep(10 * time.Millisecond)

	return server
}
