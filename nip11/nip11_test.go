package nip11

import (
	"context"
	"encoding/json"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddSupportedNIP(t *testing.T) {
	info := RelayInformationDocument{}
	info.AddSupportedNIP("12")
	info.AddSupportedNIP("12")
	info.AddSupportedNIP("13")
	info.AddSupportedNIP("1")
	info.AddSupportedNIP("12")
	info.AddSupportedNIP("44")
	info.AddSupportedNIP("2")
	info.AddSupportedNIP("13")
	info.AddSupportedNIP("2")
	info.AddSupportedNIP("13")
	info.AddSupportedNIP("0")
	info.AddSupportedNIP("17")
	info.AddSupportedNIP("19")
	info.AddSupportedNIP("1")
	info.AddSupportedNIP("18")
	info.AddSupportedNIP("FE")

	assert.Contains(t, info.SupportedNIPs, 0, 1, 2, 12, 13, 17, 18, 19, 44)
	assert.Contains(t, info.SupportedNIPs, "FE")
}

func TestAddSupportedNIPs(t *testing.T) {
	info := RelayInformationDocument{}
	info.AddSupportedNIPs([]string{"0", "1", "2", "12", "13", "17", "18", "19", "44"})

	assert.Contains(t, info.SupportedNIPs, 0, 1, 2, 12, 13, 17, 18, 19, 44)
}

func TestMarshalUnmarshal(t *testing.T) {
	pk := nostr.PubKey{1, 2, 3}

	info := RelayInformationDocument{
		Name:          "test relay",
		Description:   "a test",
		PubKey:        &pk,
		Contact:       "admin@test.com",
		Software:      "khatru",
		Version:       "1.0",
		SupportedNIPs: []any{1, 11, "FE"},
		Limitation: &RelayLimitationDocument{
			MaxMessageLength:    10000,
			AuthRequired:        true,
			CreatedAtLowerLimit: 1000,
		},
		Fees: &RelayFeesDocument{
			Admission: []struct {
				Amount int
				Unit   string
			}{{Amount: 100, Unit: "sat"}},
		},
		Retention: []*RelayRetentionDocument{
			{Time: 3600, Kinds: [][]int{{0}, {1, 2}}},
		},
		Icon:            "icon.png",
		Banner:          "banner.png",
		SupportedGrasps: []string{"grasp1"},
	}

	b, err := json.Marshal(info)
	require.NoError(t, err)

	var decoded RelayInformationDocument
	err = json.Unmarshal(b, &decoded)
	require.NoError(t, err)

	assert.Equal(t, info.Name, decoded.Name)
	assert.Equal(t, info.Description, decoded.Description)
	assert.Equal(t, info.PubKey, decoded.PubKey)
	assert.Equal(t, info.Contact, decoded.Contact)
	assert.Equal(t, info.Software, decoded.Software)
	assert.Equal(t, info.Version, decoded.Version)
	assert.Equal(t, info.SupportedNIPs, decoded.SupportedNIPs)
	assert.Equal(t, info.Icon, decoded.Icon)
	assert.Equal(t, info.Banner, decoded.Banner)
	assert.Equal(t, info.SupportedGrasps, decoded.SupportedGrasps)
	require.NotNil(t, decoded.Limitation)
	assert.Equal(t, info.Limitation.MaxMessageLength, decoded.Limitation.MaxMessageLength)
	assert.Equal(t, info.Limitation.AuthRequired, decoded.Limitation.AuthRequired)
	assert.Equal(t, info.Limitation.CreatedAtLowerLimit, decoded.Limitation.CreatedAtLowerLimit)
	require.NotNil(t, decoded.Fees)
	assert.Equal(t, info.Fees.Admission[0].Amount, decoded.Fees.Admission[0].Amount)
	assert.Equal(t, info.Fees.Admission[0].Unit, decoded.Fees.Admission[0].Unit)
	require.Len(t, decoded.Retention, 1)
	assert.Equal(t, info.Retention[0].Time, decoded.Retention[0].Time)
	assert.Equal(t, info.Retention[0].Kinds, decoded.Retention[0].Kinds)
}

func TestMarshalOmitEmpty(t *testing.T) {
	info := RelayInformationDocument{}

	b, err := json.Marshal(info)
	require.NoError(t, err)
	assert.Equal(t, `{}`, string(b))
}

func TestUnmarshalNullPubKey(t *testing.T) {
	j := `{"pubkey":null}`
	var info RelayInformationDocument
	err := json.Unmarshal([]byte(j), &info)
	require.NoError(t, err)
	assert.Nil(t, info.PubKey)
}

func TestMalformedUnknownFields(t *testing.T) {
	j := `{"name":"test","unknown_field":"hello","another_unknown":42,"nested":{"a":1}}`
	var info RelayInformationDocument
	err := json.Unmarshal([]byte(j), &info)
	require.NoError(t, err)
	assert.Equal(t, "test", info.Name)
	assert.Equal(t, "hello", info.Malformed["unknown_field"])
	assert.Equal(t, float64(42), info.Malformed["another_unknown"])
	require.NotNil(t, info.Malformed["nested"])

	b, err := json.Marshal(info)
	require.NoError(t, err)

	var decoded RelayInformationDocument
	err = json.Unmarshal(b, &decoded)
	require.NoError(t, err)
	assert.Equal(t, "test", decoded.Name)
	assert.Equal(t, "hello", decoded.Malformed["unknown_field"])
}

func TestMalformedFallbackOnEncode(t *testing.T) {
	info := RelayInformationDocument{
		Malformed: map[string]any{"name": "from-malformed"},
	}
	b, err := json.Marshal(info)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"name":"from-malformed"`)
}

func TestFetch(t *testing.T) {
	tests := []struct {
		inputURL     string
		expectError  bool
		expectedName string
		expectedURL  string
	}{
		{"wss://nostr.wine", false, "", "wss://nostr.wine"},
		{"https://nostr.wine", false, "", "wss://nostr.wine"},
		{"nostr.wine", false, "", "wss://nostr.wine"},
		{"relay.damus.io", false, "", "wss://relay.damus.io"},
		{"https://relay.damus.io", false, "", "wss://relay.damus.io"},
		{"wss://relay.damus.io", false, "", "wss://relay.damus.io"},
		{"wlenwqkeqwe.asjdaskd", true, "", "wss://wlenwqkeqwe.asjdaskd"},
	}

	for _, test := range tests {
		res, err := Fetch(context.Background(), test.inputURL)

		if test.expectError {
			assert.Error(t, err, "expected error for URL: %s", test.inputURL)
			assert.NotNil(t, res, "expected result not to be nil for URL: %s", test.inputURL)
			assert.Equal(t, test.expectedURL, res.URL, "expected URL to be %s for input: %s", test.expectedURL, test.inputURL)
		} else {
			assert.Nil(t, err, "unexpect error for URL: %s", test.inputURL)
			assert.NotEmpty(t, res.Name, "expected non-empty name for URL: %s", test.inputURL)
			assert.Equal(t, test.expectedURL, res.URL, "expected URL to be %s for input: %s", test.expectedURL, test.inputURL)
		}
	}
}
