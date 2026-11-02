package mail

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/mail/mailtest"
)

var newsletter = "From: News <news@a.com>\r\nSubject: Hi\r\nMIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=b\r\n\r\n" +
	"--b\r\nContent-Type: multipart/alternative; boundary=c\r\n\r\n" +
	"--c\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nline one\r\nline two\r\n" +
	"--c\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<b>Hello</b><img src=\"cid:Logo@a\"><img src=\"cid:svg@a\">\r\n" +
	"--c--\r\n" +
	"--b\r\nContent-Type: image/png\r\nContent-Id: <logo@a>\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=l.png\r\n\r\n" +
	base64.StdEncoding.EncodeToString([]byte("picture")) + "\r\n" +
	"--b\r\nContent-Type: image/svg+xml\r\nContent-Id: <svg@a>\r\n\r\n<svg onload=\"x()\"/>\r\n--b--\r\n"

func TestAMessageKeepsItsPageAndInlinePictures(t *testing.T) {
	p := Parse([]byte(newsletter))
	if p.Text != "line one\nline two" {
		t.Errorf("text %q still has the wire's line ends", p.Text)
	}
	if !strings.Contains(p.HTML, "<b>Hello</b>") {
		t.Errorf("page %q", p.HTML)
	}
	if want := `src="data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("picture")) + `"`; !strings.Contains(p.HTML, want) {
		t.Errorf("the inline picture is not in the page: %q", p.HTML)
	}
	if !strings.Contains(p.HTML, `cid:svg@a`) || strings.Contains(p.HTML, "onload") {
		t.Errorf("an SVG was taken inline: %q", p.HTML)
	}
}

func TestAPageAllowsNoScriptAndPicturesOnlyWhenAsked(t *testing.T) {
	for _, pictures := range []bool{false, true} {
		policy := PagePolicy(pictures)
		for _, must := range []string{"default-src 'none'", "script-src 'none'", "frame-src 'none'", "connect-src 'none'", "form-action 'none'", "base-uri 'none'"} {
			if !strings.Contains(policy, must) {
				t.Errorf("policy %q lacks %q", policy, must)
			}
		}
		if strings.Contains(policy, "unsafe-eval") || strings.Contains(policy, "'unsafe-inline'") && !strings.Contains(policy, "style-src 'unsafe-inline'") {
			t.Errorf("policy %q opens more than styles", policy)
		}
		if strings.Contains(policy, "https:") != pictures {
			t.Errorf("policy %q with pictures %t", policy, pictures)
		}
	}
	page := Page(`<meta charset="gb2312"><script>evil()</script>`, false)
	if strings.Index(page, "Content-Security-Policy") > strings.Index(page, "evil") || strings.Index(page, `charset="utf-8"`) > strings.Index(page, "gb2312") {
		t.Errorf("the message comes before the page's own lines: %q", page)
	}
}

func TestRemoteContentIsNoticed(t *testing.T) {
	for html, want := range map[string]bool{
		`<img src="https://t.co/p.gif">`:                   true,
		`<img SRC = 'http://t.co/p.gif'>`:                  true,
		`<img src=//t.co/p.gif>`:                           true,
		`<td background="https://a/b.png">`:                true,
		`<div style="background:url(https://a/b.png)">`:    true,
		`<img srcset="https://a/b.png 2x">`:                true,
		`<a href="https://a/b">link</a>`:                   false,
		`<img src="data:image/png;base64,AAAA">`:           false,
		`<p>See https://a/b.png for the src of it all</p>`: false,
	} {
		if got := HasRemote(html); got != want {
			t.Errorf("HasRemote(%q) = %t", html, got)
		}
	}
}

func TestPicturesLeftOutLeaveTheirWordsAndNoBrokenMark(t *testing.T) {
	rich := `<img src="https://a/logo.png" alt="Chase &amp; Co" width="600"><IMG alt='' SRC=https://a/spacer.gif>` +
		`<img src=//t.co/p.gif><img alt="&lt;b&gt;" src="https://a/b.png"><img src="data:image/png;base64,AAAA" alt="kept">`
	page := Page(rich, false)
	for _, gone := range []string{"https://a", "t.co", "spacer", `width="600"`, "<b>"} {
		if strings.Contains(page, gone) {
			t.Errorf("%q is in %q", gone, page)
		}
	}
	for _, kept := range []string{"Chase &amp; Co", "&lt;b&gt;", `src="data:image/png;base64,AAAA" alt="kept"`} {
		if !strings.Contains(page, kept) {
			t.Errorf("%q is not in %q", kept, page)
		}
	}
	if with := Page(rich, true); !strings.Contains(with, "https://a/logo.png") {
		t.Errorf("the pictures were asked for: %q", with)
	}
}

func TestAPageKeepsToItsLightColoursOnADarkSystem(t *testing.T) {
	page := Page(`<style>@media (prefers-color-scheme: dark){td{background:#111}}@media (PREFERS-COLOR-SCHEME:DARK){a{color:#fff}}</style>`, false)
	if strings.Contains(strings.ToLower(page), "color-scheme: dark") || strings.Contains(strings.ToLower(page), "color-scheme:dark") {
		t.Errorf("the dark styles still apply: %q", page)
	}
}

func TestAPreviewHasNoAddressesOrRules(t *testing.T) {
	text := "Tesla [https://link.tesla.com/ls/click?upn=abc]\n\nGet a month -->\n|||||||||| ---------- (https://a.b/c) free."
	if got := Preview(text, 120); got != "Tesla Get a month free." {
		t.Errorf("%q", got)
	}
}

func TestPagesAreFilledInForEarlierMessagesOnce(t *testing.T) {
	s := mailtest.New(t, false)
	m := testMailer(t)
	a := accountOf("a", s)
	for i := range fetchBatch + 3 {
		s.Add("News <news@a.com>", fmt.Sprintf("Letter %d", i), "").Raw = newsletter
	}
	if _, err := m.Sync(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.db.Exec("UPDATE messages SET html='', whole=0, paged=0"); err != nil {
		t.Fatal(err)
	}
	var seen []int
	if err := m.FillPages(context.Background(), a, func(done, total int) {
		seen = append(seen, done)
		if total != fetchBatch+3 {
			t.Errorf("total %d", total)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []int{fetchBatch, fetchBatch + 3}) {
		t.Errorf("progress %v", seen)
	}
	list, _ := m.Store.List(Filter{})
	for _, msg := range list {
		if b, err := m.Store.Body(msg.ID); err != nil || !b.Whole || !strings.Contains(b.HTML, "<b>Hello</b>") {
			t.Fatalf("%+v %v", b, err)
		}
	}
	dials := 0
	dial := m.Dial
	m.Dial = func(ctx context.Context, address string) (*Conn, error) {
		dials++
		return dial(ctx, address)
	}
	if err := m.FillPages(context.Background(), a, nil); err != nil || dials != 0 {
		t.Errorf("a second run went to the server %d times: %v", dials, err)
	}
}

func TestAMessageWithoutAPageIsNotAskedForAgain(t *testing.T) {
	s := mailtest.New(t, false)
	m := testMailer(t)
	a := accountOf("a", s)
	s.Add("Ann <ann@a.com>", "Plain", "Just words")
	if _, err := m.Sync(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
	if rows, err := m.Store.unpaged(a.ID); err != nil || len(rows) != 0 {
		t.Errorf("%v %v", rows, err)
	}
}

func TestACutMessageIsCompletedWhenOpened(t *testing.T) {
	s := mailtest.New(t, false)
	m := testMailer(t)
	a := accountOf("a", s)
	raw := newsletter + strings.Repeat("x", bodyPart)
	s.Add("News <news@a.com>", "Hi", "").Raw = raw
	if _, err := m.Sync(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
	list, err := m.Store.List(Filter{})
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if b, err := m.Store.Body(list[0].ID); err != nil || b.Whole {
		t.Fatalf("a message of more than bodyPart counts as whole: %+v %v", b, err)
	}
	b, err := m.Complete(context.Background(), a, list[0].ID)
	if err != nil || !b.Whole || !strings.Contains(b.HTML, "data:image/png") {
		t.Fatalf("%+v %v", b, err)
	}
	if got, err := m.Store.Body(list[0].ID); err != nil || got != b {
		t.Errorf("not kept: %+v %v", got, err)
	}
}

func TestAMessageLeftTheInboxStaysAsItIs(t *testing.T) {
	s := mailtest.New(t, false)
	m := testMailer(t)
	a := accountOf("a", s)
	s.Add("News <news@a.com>", "Hi", "").Raw = newsletter + strings.Repeat("x", bodyPart)
	if _, err := m.Sync(context.Background(), a, nil); err != nil {
		t.Fatal(err)
	}
	list, _ := m.Store.List(Filter{})
	if err := m.Store.setInbox(list[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if b, err := m.Complete(context.Background(), a, list[0].ID); err != nil || b.Whole {
		t.Errorf("%+v %v", b, err)
	}
}

func TestAnEarlierDatabaseIsBroughtUpToDate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema + `INSERT INTO messages (account, validity, uid, body) VALUES ('a', 1, 1, 'one` + "\r\n" + `two')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 { // and opening it again changes nothing
		store, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		list, err := store.List(Filter{})
		if err != nil || len(list) != 1 || list[0].Text != "one\ntwo" {
			t.Fatal(list, err)
		}
		if b, err := store.Body(list[0].ID); err != nil || b.Whole || b.HTML != "" {
			t.Errorf("%+v %v", b, err)
		}
		if rows, err := store.unpaged("a"); err != nil || len(rows) != 1 {
			t.Errorf("%v %v", rows, err)
		}
		store.Close()
	}
}

func TestPicturesAreTrustedByAddress(t *testing.T) {
	m := testMailer(t)
	if ok, err := m.Store.Trusted("a@b.com"); err != nil || ok {
		t.Fatal(ok, err)
	}
	for range 2 {
		if err := m.Store.Trust("A@b.com"); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := m.Store.Trusted("a@B.com"); err != nil || !ok {
		t.Error(ok, err)
	}
}
