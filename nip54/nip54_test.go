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
		{name: "example 1", input: "_This is *regular_ not strong* emphasis", contains: []string{"<p><em>This is *regular</em> not strong* emphasis</p>"}},
		{name: "example 2", input: "*This is _strong* not regular_ emphasis", contains: []string{"<p><strong>This is _strong</strong> not regular_ emphasis</p>"}},
		{name: "example 3", input: "[Link *](url)*", contains: []string{"<p><a href=\"url\">Link *</a>*</p>"}},
		{name: "example 4", input: "*Emphasis [*](url)", contains: []string{"<p><strong>Emphasis [</strong>](url)</p>"}},
		{name: "example 5", input: "_This is *strong within* regular emphasis_", contains: []string{"<p><em>This is <strong>strong within</strong> regular emphasis</em></p>"}},
		{name: "example 6", input: "{_Emphasized_}\n_}not emphasized{_", contains: []string{"<p><em>Emphasized</em>\n_}not emphasized{_</p>"}},
		{name: "example 7", input: "*not strong *strong*", contains: []string{"<p>*not strong <strong>strong</strong></p>"}},
		{name: "example 8", input: "[My link text](http://example.com)", contains: []string{"<p><a href=\"http://example.com\">My link text</a></p>"}},
		{name: "example 9", input: "[My link text](http://example.com?product_number=234234234234\n234234234234)", contains: []string{"<p><a href=\"http://example.com?product_number=234234234234234234234234\">My link text</a></p>"}},
		{name: "example 10", input: "[My link text][foo bar]\n\n[foo bar]: http://example.com", contains: []string{"<p><a href=\"http://example.com\">My link text</a></p>"}},
		{name: "example 11", input: "[foo][bar]", contains: []string{"<p><a>foo</a></p>"}},
		{name: "example 12", input: "[My link text][]\n\n[My link text]: /url", contains: []string{"<p><a href=\"/url\">My link text</a></p>"}},
		{name: "example 13", input: "![picture of a cat](cat.jpg)\n\n![picture of a cat][cat]\n\n![cat][]\n\n[cat]: feline.jpg", contains: []string{"<p><img alt=\"picture of a cat\" src=\"cat.jpg\"></p>\n<p><img alt=\"picture of a cat\" src=\"feline.jpg\"></p>\n<p><img alt=\"cat\" src=\"feline.jpg\"></p>"}},
		{name: "example 14", input: "<https://pandoc.org/lua-filters>\n<me@example.com>", contains: []string{"<p><a href=\"https://pandoc.org/lua-filters\">https://pandoc.org/lua-filters</a>\n<a href=\"mailto:me@example.com\">me@example.com</a></p>"}},
		{name: "example 15", input: "``Verbatim with a backtick` character``\n`Verbatim with three backticks ``` character`", contains: []string{"<p><code>Verbatim with a backtick` character</code>\n<code>Verbatim with three backticks ``` character</code></p>"}},
		{name: "example 16", input: "`` `foo` ``", contains: []string{"<p><code>`foo`</code></p>"}},
		{name: "example 17", input: "`foo bar", contains: []string{"<p><code>foo bar</code></p>"}},
		{name: "example 18", input: "_emphasized text_\n\n*strong emphasis*", contains: []string{"<p><em>emphasized text</em></p>\n<p><strong>strong emphasis</strong></p>"}},
		{name: "example 19", input: "_ Not emphasized (spaces). _\n\n___ (not an emphasized `_` character)", contains: []string{"<p>_ Not emphasized (spaces). _</p>\n<p>___ (not an emphasized <code>_</code> character)</p>"}},
		{name: "example 20", input: "__emphasis inside_ emphasis_", contains: []string{"<p><em><em>emphasis inside</em> emphasis</em></p>"}},
		{name: "example 21", input: "{_ this is emphasized, despite the spaces! _}", contains: []string{"<p><em> this is emphasized, despite the spaces! </em></p>"}},
		{name: "example 22", input: "This is {=highlighted text=}.", contains: []string{"<p>This is <mark>highlighted text</mark>.</p>"}},
		{name: "example 23", input: "H~2~O and djot^TM^", contains: []string{"<p>H<sub>2</sub>O and djot<sup>TM</sup></p>"}},
		{name: "example 24", input: "H{~one two buckle my shoe~}O", contains: []string{"<p>H<sub>one two buckle my shoe</sub>O</p>"}},
		{name: "example 25", input: "My boss is {-mean-}{+nice+}.", contains: []string{"<p>My boss is <del>mean</del><ins>nice</ins>.</p>"}},
		{name: "example 26", input: "\"Hello,\" said the spider.\n\"'Shelob' is my name.\"", contains: []string{"<p>&ldquo;Hello,&rdquo; said the spider.\n&ldquo;&lsquo;Shelob&rsquo; is my name.&rdquo;</p>"}},
		{name: "example 27", input: "'}Tis Socrates' season to be jolly!", contains: []string{"<p>&rsquo;Tis Socrates&rsquo; season to be jolly!</p>"}},
		{name: "example 28", input: "5\\'11\\\"", contains: []string{"<p>5'11\"</p>"}},
		{name: "example 29", input: "57--33 oxen---and no sheep...", contains: []string{"<p>57&ndash;33 oxen&mdash;and no sheep&hellip;</p>"}},
		{name: "example 30", input: "a----b c------d", contains: []string{"<p>a&ndash;&ndash;b c&mdash;&mdash;d</p>"}},
		{name: "example 31", input: "Einstein derived $`e=mc^2`.\nPythagoras proved\n$$` x^n + y^n = z^n `", contains: []string{"<p>Einstein derived <span class=\"math inline\">\\(e=mc^2\\)</span>.\nPythagoras proved\n<span class=\"math display\">\\[ x^n + y^n = z^n \\]</span></p>"}},
		{name: "example 32", input: "Here is the reference.[^foo]\n\n[^foo]: And here is the note.", contains: []string{"<p>Here is the reference.<a id=\"fnref1\" href=\"#fn1\" role=\"doc-noteref\"><sup>1</sup></a></p>\n<section role=\"doc-endnotes\">\n<hr>\n<ol>\n<li id=\"fn1\">\n<p>And here is the note.<a href=\"#fnref1\" role=\"doc-backlink\">↩︎︎</a></p>\n</li>\n</ol>\n</section>"}},
		{name: "example 33", input: "This is a soft\nbreak and this is a hard\\\\\\nbreak.", contains: []string{"This is a soft", "break and this is a hard", "break."}},
		{name: "example 34", input: "{#ident % later we'll add a class %}", contains: []string{""}},
		{name: "example 35", input: "Foo bar {% This is a comment, spanning\nmultiple lines %} baz.", contains: []string{"<p>Foo bar  baz.</p>"}},
		{name: "example 36", input: "My reaction is :+1: :smiley:.", contains: []string{"<p>My reaction is :+1: :smiley:.</p>"}},
		{name: "example 37", input: "This is `<?php echo 'Hello world!' ?>`{=html}.", contains: []string{"<p>This is <?php echo 'Hello world!' ?>.</p>"}},
		{name: "example 38", input: "It can be helpful to [read the manual]{.big .red}.", contains: []string{"<p>It can be helpful to <span class=\"big red\">read the manual</span>.</p>"}},
		{name: "example 39", input: "An attribute on _emphasized text_{#foo\n.bar .baz key=\"my value\"}", contains: []string{"<p>An attribute on <em class=\"bar baz\" id=\"foo\" key=\"my value\">emphasized text</em></p>"}},
		{name: "example 40", input: "avant{lang=fr}{.blue}", contains: []string{"<p><span class=\"blue\" lang=\"fr\">avant</span></p>"}},
		{name: "example 41", input: "avant{lang=fr .blue}", contains: []string{"<p><span class=\"blue\" lang=\"fr\">avant</span></p>"}},
		{name: "example 42", input: "## A level _two_ heading!", contains: []string{"<section id=\"A-level-two-heading\">\n<h2>A level <em>two</em> heading!</h2>\n</section>"}},
		{name: "example 43", input: "# A heading that\n# takes up\n# three lines\n\nA paragraph, finally", contains: []string{"<section id=\"A-heading-that-takes-up-three-lines\">\n<h1>A heading that\ntakes up\nthree lines</h1>\n<p>A paragraph, finally</p>\n</section>"}},
		{name: "example 44", input: "# A heading that\ntakes up\nthree lines\n\nA paragraph, finally.", contains: []string{"<section id=\"A-heading-that-takes-up-three-lines\">\n<h1>A heading that\ntakes up\nthree lines</h1>\n<p>A paragraph, finally.</p>\n</section>"}},
		{name: "example 45", input: "> This is a block quote.\n>\n> 1. with a\n> 2. list in it.", contains: []string{"<blockquote>\n<p>This is a block quote.</p>\n<ol>\n<li>\nwith a\n</li>\n<li>\nlist in it.\n</li>\n</ol>\n</blockquote>"}},
		{name: "example 46", input: "> This is a block\nquote.", contains: []string{"<blockquote>\n<p>This is a block\nquote.</p>\n</blockquote>"}},
		{name: "example 47", input: "1.  This is a\n list item.\n\n > containing a block quote", contains: []string{"<ol>\n<li>\n<p>This is a\nlist item.</p>\n<blockquote>\n<p>containing a block quote</p>\n</blockquote>\n</li>\n</ol>"}},
		{name: "example 48", input: "1.  This is a\nlist item.\n\n  Second paragraph under the\nlist item.", contains: []string{"<ol>\n<li>\n<p>This is a\nlist item.</p>\n<p>Second paragraph under the\nlist item.</p>\n</li>\n</ol>"}},
		{name: "example 49", input: ": orange\n\n  A citrus fruit.", contains: []string{"<dl>\n<dt>orange</dt>\n<dd>\n<p>A citrus fruit.</p>\n</dd>\n</dl>"}},
		{name: "example 50", input: "i) one\ni. one (style change)\n+ bullet\n* bullet (style change)", contains: []string{"<ol start=\"9\" type=\"a\">\n<li>\none\n</li>\n</ol>\n<ol start=\"9\" type=\"a\">\n<li>\none (style change)\n</li>\n</ol>\n<ul>\n<li>\nbullet\n</li>\n</ul>\n<ul>\n<li>\nbullet (style change)\n</li>\n</ul>"}},
		{name: "example 51", input: "i. item\nj. next item", contains: []string{"<ol start=\"9\" type=\"a\">\n<li>\nitem\n</li>\n<li>\nnext item\n</li>\n</ol>"}},
		{name: "example 52", input: "5) five\n8) six", contains: []string{"<ol start=\"5\">\n<li>\nfive\n</li>\n<li>\nsix\n</li>\n</ol>"}},
		{name: "example 53", input: "- one\n- two\n\n  - sub\n  - sub", contains: []string{"<ul>\n<li>\none\n</li>\n<li>\ntwo\n<ul>\n<li>\nsub\n</li>\n<li>\nsub\n</li>\n</ul>\n</li>\n</ul>"}},
		{name: "example 54", input: "- one\n\n- two", contains: []string{"<ul>\n<li>\n<p>one</p>\n</li>\n<li>\n<p>two</p>\n</li>\n</ul>"}},
		{name: "example 55", input: "````\nThis is how you do a code block:\n\n``` ruby\nx = 5 * 6\n```\n````", contains: []string{"<pre><code>This is how you do a code block:\n\n``` ruby\nx = 5 * 6\n```\n</code></pre>"}},
		{name: "example 56", input: "> ```\n> code in a\n> block quote\n\nParagraph.", contains: []string{"<blockquote>\n<pre><code>code in a\nblock quote\n</code></pre>\n</blockquote>\n<p>Paragraph.</p>"}},
		{name: "example 57", input: "Then they went to sleep.\n\n      * * * *\n\nWhen they woke up, ...", contains: []string{"<p>Then they went to sleep.</p>\n<hr>\n<p>When they woke up, &hellip;</p>"}},
		{name: "example 58", input: "``` =html\n<video width=\"320\" height=\"240\" controls>\n  <source src=\"movie.mp4\" type=\"video/mp4\">\n  <source src=\"movie.ogg\" type=\"video/ogg\">\n  Your browser does not support the video tag.\n</video>\n```", contains: []string{"<video width=\"320\" height=\"240\" controls>\n  <source src=\"movie.mp4\" type=\"video/mp4\">\n  <source src=\"movie.ogg\" type=\"video/ogg\">\n  Your browser does not support the video tag.\n</video>"}},
		{name: "example 59", input: "::: warning\nHere is a paragraph.\n\nAnd here is another.\n:::", contains: []string{"<div class=\"warning\">\n<p>Here is a paragraph.</p>\n<p>And here is another.</p>\n</div>"}},
		{name: "example 60", input: "| 1 | 2 |", contains: []string{"<table>\n<tr>\n<td>1</td>\n<td>2</td>\n</tr>\n</table>"}},
		{name: "example 61", input: "| fruit  | price |\n|--------|------:|\n| apple  |     4 |\n| banana |    10 |", contains: []string{"<table>\n<tr>\n<th>fruit</th>\n<th style=\"text-align: right;\">price</th>\n</tr>\n<tr>\n<td>apple</td>\n<td style=\"text-align: right;\">4</td>\n</tr>\n<tr>\n<td>banana</td>\n<td style=\"text-align: right;\">10</td>\n</tr>\n</table>"}},
		{name: "example 62", input: "| a  |  b |\n|----|:--:|\n| 1  | 2  |\n|:---|---:|\n| 3  | 4  |", contains: []string{"<table>\n<tr>\n<th>a</th>\n<th style=\"text-align: center;\">b</th>\n</tr>\n<tr>\n<th style=\"text-align: left;\">1</th>\n<th style=\"text-align: right;\">2</th>\n</tr>\n<tr>\n<td style=\"text-align: left;\">3</td>\n<td style=\"text-align: right;\">4</td>\n</tr>\n</table>"}},
		{name: "example 63", input: "| just two \\| `|` | cells in this table |", contains: []string{"<table>\n<tr>\n<td>just two | <code>|</code></td>\n<td>cells in this table</td>\n</tr>\n</table>"}},
		{name: "example 64", input: "^ This is the caption.  It can contain _inline formatting_\n  and can extend over multiple lines, provided they are\n  indented relative to the `^`.", contains: []string{""}},
		{name: "example 65", input: "[google]: https://google.com\n\n[product page]: http://example.com?item=983459873087120394870128370\n  0981234098123048172304", contains: []string{""}},
		{name: "example 66", input: "{title=foo}\n[ref]: /url\n\n[ref][]", contains: []string{"<p><a href=\"/url\" title=\"foo\">ref</a></p>"}},
		{name: "example 67", input: "{title=foo}\n[ref]: /url\n\n[ref][]{title=bar}", contains: []string{"<p><a href=\"/url\" title=\"bar\">ref</a></p>"}},
		{name: "example 68", input: "Here's the reference.[^foo]\n\n[^foo]: This is a note\n  with two paragraphs.\n\n  Second paragraph.\n\n  > a block quote in the note.", contains: []string{"<p>Here&rsquo;s the reference.<a id=\"fnref1\" href=\"#fn1\" role=\"doc-noteref\"><sup>1</sup></a></p>\n<section role=\"doc-endnotes\">\n<hr>\n<ol>\n<li id=\"fn1\">\n<p>This is a note\nwith two paragraphs.</p>\n<p>Second paragraph.</p>\n<blockquote>\n<p>a block quote in the note.</p>\n</blockquote>\n<p><a href=\"#fnref1\" role=\"doc-backlink\">↩︎︎</a></p>\n</li>\n</ol>\n</section>"}},
		{name: "example 69", input: "Here's the reference.[^foo]\n\n[^foo]: This is a note\nwith two paragraphs.\n\n  Second paragraph must\nbe indented, at least in the first line.", contains: []string{"<p>Here&rsquo;s the reference.<a id=\"fnref1\" href=\"#fn1\" role=\"doc-noteref\"><sup>1</sup></a></p>\n<section role=\"doc-endnotes\">\n<hr>\n<ol>\n<li id=\"fn1\">\n<p>This is a note\nwith two paragraphs.</p>\n<p>Second paragraph must\nbe indented, at least in the first line.<a href=\"#fnref1\" role=\"doc-backlink\">↩︎︎</a></p>\n</li>\n</ol>\n</section>"}},
		{name: "example 70", input: "{#water}\n{.important .large}\nDon't forget to turn off the water!\n\n{source=\"Iliad\"}\n> Sing, muse, of the wrath of Achilles", contains: []string{"<p class=\"important large\" id=\"water\">Don&rsquo;t forget to turn off the water!</p>\n<blockquote source=\"Iliad\">\n<p>Sing, muse, of the wrath of Achilles</p>\n</blockquote>"}},
		{name: "example 71", input: "## My heading + auto-identifier", contains: []string{"<section id=\"My-heading-auto-identifier\">\n<h2>My heading + auto-identifier</h2>\n</section>"}},
		{name: "example 72", input: "See the [Epilogue][].\n\n    * * * *\n\n# Epilogue", contains: []string{"<p>See the <a href=\"#Epilogue\">Epilogue</a>.</p>\n<hr>\n<section id=\"Epilogue\">\n<h1>Epilogue</h1>\n</section>"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ArticleAsHTML(tt.input)
			t.Log("[input]: " + tt.input)
			t.Log("[result]: " + result)
			for _, expected := range tt.contains {
				t.Log("[expected]: " + expected)
				if !strings.Contains(result, expected) {
					t.Errorf("ArticleAsHTML() output does not contain %q\nGot: %s", expected, result)
				}
			}
		})
	}
}
