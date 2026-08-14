package telegram

import (
	"testing"
)

func TestProcessTextAndParseMode(t *testing.T) {
	input := "**Hello World** and `code here`"
	processed, mode := ProcessTextAndParseMode(input, "")

	if mode != "HTML" {
		t.Errorf("Expected HTML mode, got %s", mode)
	}
	if processed != "<b>Hello World</b> and <code>code here</code>" {
		t.Errorf("Unexpected output: %s", processed)
	}

	htmlInput := "Tom & Jerry <cartoon>"
	processedHTML, _ := ProcessTextAndParseMode(htmlInput+" **bold**", "")
	if processedHTML != "Tom &amp; Jerry &lt;cartoon&gt; <b>bold</b>" {
		t.Errorf("Expected escaped HTML with bold tag, got %s", processedHTML)
	}
}
