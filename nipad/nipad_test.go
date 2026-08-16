package nipad

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	pk := nostr.Generate().Public()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/.well-known/nostr.json", r.URL.Path)
		assert.Equal(t, "/players", r.URL.Query().Get("path"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"/players":{"filter":{"kinds":[30000],"#d":["players"],"authors":["` + pk.Hex() + `"],"limit":1},"relays":["wss://relay.golf.com"]}}`))
	}))
	defer server.Close()

	entry, err := Resolve(context.Background(), server.URL+"/players")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, []nostr.Kind{30000}, entry.Filter.Kinds)
	assert.Equal(t, []string{"players"}, entry.Filter.Tags["d"])
	assert.Equal(t, []nostr.PubKey{pk}, entry.Filter.Authors)
	assert.Equal(t, 1, entry.Filter.Limit)
	assert.Equal(t, []string{"wss://relay.golf.com"}, entry.Relays)
}

func TestResolveNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"/other":{"filter":{}}}`))
	}))
	defer server.Close()

	entry, err := Resolve(context.Background(), server.URL+"/players")
	assert.Error(t, err)
	assert.Nil(t, entry)
}

func TestJSON(t *testing.T) {
	resp := WellKnownResponse{
		"/players": {
			Filter: nostr.Filter{Kinds: []nostr.Kind{30000}, Limit: 1},
			Relays: []string{"wss://relay.golf.com"},
		},
	}

	j, err := resp.MarshalJSON()
	require.NoError(t, err)

	var back WellKnownResponse
	require.NoError(t, back.UnmarshalJSON(j))
	require.Equal(t, resp, back)
}
