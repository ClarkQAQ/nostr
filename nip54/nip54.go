package nip54

import (
	"strings"
	"unicode"

	"github.com/sivukhin/godjot/djot_parser"
	"github.com/sivukhin/godjot/html_writer"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

func NormalizeIdentifier(name string) string {
	res, _, _ := transform.Bytes(norm.NFKC, []byte(name))
	runes := []rune(strings.ToLower(string(res)))

	words := make([]string, 0, 3)
	word := make([]rune, 0, 12)
	for _, letter := range runes {
		if unicode.IsLetter(letter) || unicode.IsNumber(letter) {
			word = append(word, letter)
		} else if len(word) > 0 {
			words = append(words, string(word))
			word = make([]rune, 0, 12)
		}
	}
	if len(word) > 0 {
		words = append(words, string(word))
	}

	return strings.Join(words, "-")
}

func ArticleAsHTML(content string) string {
	ast := djot_parser.BuildDjotAst([]byte(content))
	context := djot_parser.NewConversionContext("html", djot_parser.DefaultConversionRegistry)
	writer := &html_writer.HtmlWriter{}
	for _, node := range ast {
		context.ConvertDjotToHtml(writer, node)
	}
	return writer.String()
}
