package nip54

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalization(t *testing.T) {
	for _, vector := range []struct {
		before string
		after  string
	}{
		{" hello  ", "hello"},
		{"Goodbye", "goodbye"},
		{"the long and winding road / that leads to your door", "the-long-and-winding-road---that-leads-to-your-door"},
		{"it's 平仮名", "it-s-平仮名"},
	} {
		if norm := NormalizeIdentifier(vector.before); norm != vector.after {
			fmt.Println([]byte(vector.after), []byte(norm))
			t.Fatalf("%s: %s != %s", vector.before, norm, vector.after)
		}
	}
}

func TestArticleAsHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
	}{
		{
			name:     "simple paragraph",
			input:    "Hello world",
			contains: []string{"<p>", "Hello world", "</p>"},
		},
		{
			name:     "emphasis",
			input:    "*Hello* _world_",
			contains: []string{"<strong>", "Hello", "</strong>", "<em>", "world", "</em>"},
		},
		{
			name:     "heading",
			input:    "# Title",
			contains: []string{"<h1>", "Title", "</h1>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ArticleAsHTML(tt.input)
			for _, expected := range tt.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("ArticleAsHTML() output does not contain %q\nGot: %s", expected, result)
				}
			}
		})
	}
}
