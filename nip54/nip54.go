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
	name = strings.TrimSpace(strings.ToLower(name))
	res, _, _ := transform.Bytes(norm.NFKC, []byte(name))
	runes := []rune(string(res))

	b := make([]rune, len(runes))
	for i, letter := range runes {
		if unicode.IsLetter(letter) || unicode.IsNumber(letter) {
			b[i] = letter
		} else {
			b[i] = '-'
		}
	}

	return string(b)
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
