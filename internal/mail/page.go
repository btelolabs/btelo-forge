package mail

import (
	"html"
	"regexp"
	"strings"
)

// A message's HTML is a stranger's page, so it is shown with nothing that could act: no script, no frame, no form, and
// nothing fetched from outside until the reader asks for pictures. Each view holds to this at its own level as well
// (the page's script switch and a filter on every request), so a mistake in one of them lets nothing through.

// PagePolicy is the Content-Security-Policy of a message's page.
func PagePolicy(pictures bool) string {
	images := "data:"
	if pictures {
		images += " https: http:"
	}
	return "default-src 'none'; script-src 'none'; object-src 'none'; frame-src 'none'; connect-src 'none'; media-src 'none'; " +
		"manifest-src 'none'; worker-src 'none'; base-uri 'none'; form-action 'none'; style-src 'unsafe-inline'; font-src data:; img-src " + images
}

// pageStyle comes before the message's own styles, which may replace it: mail is written for a white page. What is
// important in it is what a message must not undo: most mail is laid out 600 pixels wide and runs off a narrower reader,
// and most sets its margins to nothing, for a client that adds its own.
const pageStyle = `<style>:root{color-scheme:light!important}html{background:#fff;color:#1f2328;font:14px/1.5 "Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}` +
	`html body{margin:0!important;padding:14px 18px!important;box-sizing:border-box!important;min-width:0!important;width:auto!important;overflow-wrap:anywhere}` +
	`body table,body tbody,body tr,body td,body th,body div,body center{min-width:0!important;max-width:100%!important;box-sizing:border-box}` +
	`body table[width],body td[width],body th[width]{width:auto!important}body table[width="100%"]{width:100%!important}` +
	`img{max-width:100%!important;height:auto}pre{white-space:pre-wrap}</style>`

// PageFit is what the program itself runs in a page once it has loaded, as a script of the message never can: what is
// still wider than the reader after pageStyle, a picture or columns of a fixed width, is scaled down whole.
const PageFit = `(function(){var e=document.documentElement;e.style.zoom=1;var w=e.clientWidth,s=e.scrollWidth;if(s>w+1)e.style.zoom=Math.max(w/s,.4)})()`

// darkStyles are a message's styles for a dark system. They are half of a look in most mail, dark blocks on the white of
// the rest, and the reader's page is white whatever the system's colours are.
var darkStyles = regexp.MustCompile(`(?i)\(\s*prefers-color-scheme\s*:\s*dark\s*\)`)

var (
	imageTag     = regexp.MustCompile(`(?is)<img\b(?:[^>"']|"[^"]*"|'[^']*')*>`)
	imageWords   = regexp.MustCompile(`(?is)\salt\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	remoteSource = regexp.MustCompile(`(?is)\s(?:src|srcset)\s*=\s*(?:"\s*(?:https?:)?//[^"]*"|'\s*(?:https?:)?//[^']*'|(?:https?:)?//[^\s>]*)`)
)

// withoutRemotePictures leaves of a picture from outside its alternative text, or nothing, in place of the browser's mark
// for a failed picture and the empty box around it. It is for the look of the page: what keeps pictures out is the policy
// and the view's own filter.
func withoutRemotePictures(rich string) string {
	return imageTag.ReplaceAllStringFunc(rich, func(tag string) string {
		if !remoteSource.MatchString(tag) {
			return tag
		}
		words := imageWords.FindStringSubmatch(tag)
		if words == nil {
			return ""
		}
		text := strings.TrimSpace(html.UnescapeString(words[1] + words[2]))
		if text == "" {
			return ""
		}
		return `<span style="color:#8a8f98;font-size:12px">` + html.EscapeString(text) + `</span>`
	})
}

// Page makes the page that shows a message. Its first lines come before anything of the message, so that the policy
// holds for all of it and a charset the message claims does not count: it is already UTF-8 here.
func Page(rich string, pictures bool) string {
	if !pictures {
		rich = withoutRemotePictures(rich)
	}
	rich = darkStyles.ReplaceAllString(rich, "(prefers-color-scheme:no-preference-of-this-page)")
	var b strings.Builder
	b.Grow(len(rich) + 1024)
	b.WriteString(`<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="`)
	b.WriteString(PagePolicy(pictures))
	b.WriteString(`"><meta http-equiv="x-dns-prefetch-control" content="off">`)
	b.WriteString(pageStyle)
	b.WriteString(rich)
	return b.String()
}

var remoteContent = regexp.MustCompile(`(?i)(?:\b(?:src|srcset|background|poster)\s*=\s*["']?\s*|url\(\s*["']?\s*)(?:https?:)?//`)

// HasRemote reports whether a message has pictures or other things to fetch from outside.
func HasRemote(rich string) bool { return remoteContent.MatchString(rich) }
