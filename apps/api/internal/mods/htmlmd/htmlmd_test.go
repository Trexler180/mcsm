package htmlmd

import "testing"

func TestToMarkdown(t *testing.T) {
	in := `<h2>Changes</h2><p>See <a href="https://example.com">notes</a>.</p>` +
		`<ol><li>First <em>item</em></li><li>Uses <code>foo()</code></li></ol>` +
		`<ul><li>Top<ul><li>Nested</li></ul></li></ul><pre><code>a = 1</code></pre>`
	want := "## Changes\n\n" +
		"See [notes](https://example.com).\n\n" +
		"1. First *item*\n2. Uses `foo()`\n\n" +
		"- Top\n  - Nested\n\n" +
		"```\na = 1\n```"
	if got := ToMarkdown(in); got != want {
		t.Fatalf("want:\n%s\ngot:\n%s", want, got)
	}
	// Plain-text input passes through.
	if got := ToMarkdown("just a plain note"); got != "just a plain note" {
		t.Fatalf("plain text mangled: %q", got)
	}
}

func TestToMarkdownImages(t *testing.T) {
	in := `<p><img src="https://example.com/a.png" alt="shot"></p>`
	want := "![shot](https://example.com/a.png)"
	if got := ToMarkdown(in); got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}
