package blossom

import (
	"fmt"
	"strings"
	"testing"
)

const (
	validHashLower = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	validHashUpper = "E3B0C44298FC1C149AFBF4C8996FB92427AE41E4649B934CA495991B7852B855"
	validHashMixed = "E3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func TestParseHashPath(t *testing.T) {
	tests := []struct {
		input        string
		wantHash     string
		wantMimeType string
		wantOk       bool
	}{
		{
			input:        validHashLower + ".jpg",
			wantHash:     validHashLower,
			wantMimeType: "image/jpeg",
			wantOk:       true,
		},
		{
			input:        "/" + validHashLower + ".jpg",
			wantHash:     validHashLower,
			wantMimeType: "image/jpeg",
			wantOk:       true,
		},
		{
			input:        "/data/uploads/" + validHashLower + ".jpg",
			wantHash:     validHashLower,
			wantMimeType: "image/jpeg",
			wantOk:       true,
		},
		{
			input:        "/images/" + validHashUpper,
			wantHash:     validHashUpper,
			wantMimeType: "",
			wantOk:       false,
		},
		{
			input:        validHashMixed + ".png",
			wantHash:     validHashMixed,
			wantMimeType: "image/png",
			wantOk:       false,
		},
		{
			input:        validHashLower + ".gif",
			wantHash:     validHashLower,
			wantMimeType: "image/gif",
			wantOk:       true,
		},
		{
			input:        validHashLower[:63] + ".jpg",
			wantHash:     "",
			wantMimeType: "",
			wantOk:       false,
		},
		{
			input:        validHashLower + "a.jpg",
			wantHash:     "",
			wantMimeType: "",
			wantOk:       false,
		},
		{
			input:        strings.Replace(validHashLower, "e", "g", 1) + ".jpg",
			wantHash:     "",
			wantMimeType: "",
			wantOk:       false,
		},
		{
			input:        "/path/" + validHashLower + ".tar.gz",
			wantHash:     validHashLower,
			wantMimeType: "application/x-compressed-tar",
			wantOk:       true,
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("test value %q", tt.input), func(t *testing.T) {
			gotHash, gotExt, gotOk := parseHashPath(tt.input)
			if gotOk != tt.wantOk {
				t.Errorf("parseHashPath() ok = %v, want %v", gotOk, tt.wantOk)
				return
			}
			if gotOk {
				if gotHash != tt.wantHash {
					t.Errorf("parseHashPath() hash = %v, want %v", gotHash, tt.wantHash)
				}
				if gotExt != tt.wantMimeType {
					t.Errorf("parseHashPath() mimeType = %v, want %v", gotExt, tt.wantMimeType)
				}
			}
		})
	}
}

func TestIsHashPath(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"/a/" + validHashLower + ".jpg", true},
		{"/b/" + validHashUpper, false},
		{validHashLower, true},
		{validHashLower + ".png", true},
		{"/a/" + validHashLower[:63] + ".jpg", false},
		{"/a/" + validHashLower + "a.jpg", false},
		{"/a/" + strings.Replace(validHashLower, "f", "z", 1) + ".jpg", false},
		{"/path/to/file.txt", false},
		{"/path/" + validHashLower + ".tar.gz", true},
		{"/upload", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("test value %q", tt.input), func(t *testing.T) {
			if got := isHashPath(tt.input); got != tt.want {
				t.Errorf("isHashPath() = %v, want %v (input: %s)", got, tt.want, tt.input)
			}
		})
	}
}
