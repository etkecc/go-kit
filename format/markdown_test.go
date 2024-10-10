package format

import (
	"testing"
)

func TestRenderBasicMarkdown(t *testing.T) {
	md := "**bold text**"
	expected := "<strong>bold text</strong>"
	got := Render(md)

	if got != expected {
		t.Errorf("Render() = %v, want %v", got, expected)
	}
}

func TestRenderWithParagraphWrapping(t *testing.T) {
	md := "This is a test."
	expected := "This is a test."
	got := Render(md)

	if got != expected {
		t.Errorf("Render() = %v, want %v", got, expected)
	}
}

func TestRenderWithMultipleParagraphs(t *testing.T) {
	md := "This is the first paragraph.\n\nThis is the second paragraph."
	expected := "This is the first paragraph.<br><br>This is the second paragraph."
	got := Render(md)

	if got != expected {
		t.Errorf("Render() = %v, want %v", got, expected)
	}
}

func TestRenderWithLinks(t *testing.T) {
	md := "[Link](https://example.com)"
	expected := `<a href="https://example.com" target="_blank">Link</a>`
	got := Render(md)

	if got != expected {
		t.Errorf("Render() = %v, want %v", got, expected)
	}
}
