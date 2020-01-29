package core

import (
	"context"
	"github.com/rs/zerolog"
	"golang.org/x/net/html"
	"io"
)

// ParseLinks looks for html links in an io stream. It tries to continue on any errors as if nothing was wrong until
// it encounters the end of the io stream (assuming a logger is setup on the context passed in errors will be logged
// at a warn level though).
func ParseLinks(ctx context.Context, r io.Reader) []string {
	t := html.NewTokenizer(r)
	ret := make([]string, 0)
	processing := true
	for processing {
		tt := t.Next()
		if tt == html.ErrorToken {
			err := t.Err()
			switch t.Err() {
			case io.EOF:
				processing = false
			default:
				zerolog.Ctx(ctx).Warn().Err(err).Msg("error encountered parsing document")
			}
		}

		if tt == html.StartTagToken {
			tagName, hasMoreAttrs := t.TagName()
			if string(tagName) == "a" {
				for hasMoreAttrs {
					var attr []byte
					var val []byte
					attr, val, hasMoreAttrs = t.TagAttr()
					if string(attr) == "href" {
						ret = append(ret, string(val))
					}
				}
			}
		}
	}
	return ret
}
