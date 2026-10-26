package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const ccJSON = `[{"upload_id":7,"upload_name":"Lost","user_name":"speck","user_real_name":"Speck","license_name":"Attribution (3.0)",
"license_url":"http://creativecommons.org/licenses/by/3.0/","file_page_url":"PAGE","upload_tags":",media,attribution,piano,ambient,",
"files":[{"download_url":"FILE","file_format_info":{"media-type":"audio","default-ext":"mp3","ps":"3:26"}}]}]`

func moreServer(t *testing.T, referers *[]string) *httptest.Server {
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/query":
			if r.URL.Query().Get("lic") != "by" {
				http.Error(w, "licence filter missing", 400)
				return
			}
			w.Write([]byte(strings.NewReplacer("PAGE", s.URL+"/page", "FILE", s.URL+"/file.mp3").Replace(ccJSON)))
		case r.URL.Path == "/advancedsearch.php":
			if !strings.Contains(r.URL.Query().Get("q"), "licenses/by/3.0") {
				http.Error(w, "licence filter missing", 400)
				return
			}
			w.Write([]byte(`{"response":{"numFound":1,"docs":[{"identifier":"album1","licenseurl":"http://creativecommons.org/licenses/by/3.0/"}]}}`))
		case r.URL.Path == "/metadata/album1":
			w.Write([]byte(`{"metadata":{"title":"Album","creator":["Band"]},"files":[{"name":"01 a.mp3","length":"61.5","title":"A"},{"name":"cover.jpg"},{"name":"02 b.mp3","length":"1:02"}]}`))
		case r.URL.Path == "/file.mp3":
			*referers = append(*referers, r.Referer())
			w.Write([]byte("ID3"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestCCMixterAndArchiveTracksCarryTheirCredit(t *testing.T) {
	var referers []string
	s := moreServer(t, &referers)
	l := New(t.TempDir(), sitesAt(t, s.URL))
	cc, err := l.Search(context.Background(), "ccmixter", "piano", "", 1)
	if err != nil || len(cc.Tracks) != 1 {
		t.Fatalf("%+v %v", cc, err)
	}
	tr := cc.Tracks[0]
	if tr.Duration != 206 || tr.Artist != "Speck" || tr.Genre != "piano · ambient" || !strings.Contains(tr.Credit, "by/3.0") {
		t.Fatalf("%+v", tr)
	}
	path, err := l.Download(context.Background(), tr)
	if err != nil || len(referers) != 1 || referers[0] != s.URL+"/page" {
		t.Fatalf("download %v, referers %v", err, referers)
	}
	if data, _ := os.ReadFile(path); string(data) != "ID3" {
		t.Fatal("download content")
	}
	ar, err := l.Search(context.Background(), "archive", "piano", "", 1)
	if err != nil || len(ar.Tracks) != 2 {
		t.Fatalf("%+v %v", ar, err)
	}
	if a := ar.Tracks[0]; a.Title != "A" || a.Artist != "Band" || a.Duration != 61.5 || !strings.HasSuffix(a.URL, "/download/album1/01%20a.mp3") || a.Credit == "" {
		t.Fatalf("%+v", a)
	}
	if b := ar.Tracks[1]; b.Title != "02 b" || b.Duration != 62 {
		t.Fatalf("%+v", b)
	}
}

func TestSearchAllInterleavesAndKeepsWhatWorked(t *testing.T) {
	var referers []string
	s := moreServer(t, &referers)
	l := New(t.TempDir(), sitesAt(t, s.URL))
	p, err := l.SearchAll(context.Background(), KindMusic, "piano", 1)
	if err == nil || !strings.Contains(err.Error(), "Openverse") || !strings.Contains(err.Error(), "Kevin") {
		t.Fatalf("the failing sources should be reported, got %v", err)
	}
	if len(p.Tracks) != 3 || p.Tracks[0].Provider != "ccmixter" || p.Tracks[1].Provider != "archive" {
		t.Fatalf("%+v", p.Tracks)
	}
}
