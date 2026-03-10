package render

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

func MarkdownToSafeHTML(md string) (template.HTML, error) {
	var buf bytes.Buffer

	if err := goldmark.Convert([]byte(md), &buf); err != nil {
		return "", err
	}

	policy := bluemonday.UGCPolicy()
	safeHTML := policy.Sanitize(buf.String())

	return template.HTML(safeHTML), nil
}
