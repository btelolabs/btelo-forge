// Package web serves the exported videos to phones on the local network: an htmx page listing them and the files,
// behind a random key in the address.
package web

import (
	"crypto/subtle"
	_ "embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

//go:embed static/htmx.min.js
var htmx []byte

// File is one exported video.
type File struct {
	Path string
	At   time.Time
}

// Text is the page's wording, in the app's language.
type Text struct {
	Title, Empty, Download, Refresh string
}

// Handler serves the page for files() to anyone presenting key.
func Handler(key string, files func() []File, text func() Text) http.Handler {
	mux := http.NewServeMux()
	allowed := func(w http.ResponseWriter, r *http.Request) bool {
		got := r.URL.Query().Get("k")
		if c, err := r.Cookie("k"); got == "" && err == nil {
			got = c.Value
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
		http.SetCookie(w, &http.Cookie{Name: "k", Value: key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		return true
	}
	mux.HandleFunc("GET /htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(htmx)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page.Execute(w, text())
	})
	mux.HandleFunc("GET /list", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		list.Execute(w, rows(files(), text()))
	})
	mux.HandleFunc("GET /file/{n}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		all := files()
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil || n < 0 || n >= len(all) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(all[n].Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		if r.URL.Query().Get("dl") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", template.URLQueryEscaper(filepath.Base(all[n].Path))))
		}
		http.ServeContent(w, r, filepath.Base(all[n].Path), st.ModTime(), f)
	})
	return mux
}

type row struct {
	N        int
	Name     string
	Size     string
	When     string
	Download string
}

func rows(files []File, text Text) struct {
	Rows []row
	Text Text
} {
	var out []row
	for i, f := range files {
		st, err := os.Stat(f.Path)
		if err != nil {
			continue
		}
		out = append(out, row{N: i, Name: filepath.Base(f.Path), Size: size(st.Size()), When: f.At.Format("2006-01-02 15:04"), Download: text.Download})
	}
	return struct {
		Rows []row
		Text Text
	}{out, text}
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n>>10)
}

var page = template.Must(template.New("page").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title><script src="/htmx.min.js"></script>
<style>
:root{color-scheme:dark;--bg:#0f1112;--panel:#171a1c;--line:#2b3034;--text:#eceff0;--dim:#9ba3a8;--accent:#1fbf75}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:16px/1.45 system-ui,-apple-system,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}
header{display:flex;align-items:center;justify-content:space-between;padding:18px 16px;border-bottom:1px solid var(--line)}
h1{font-size:18px;margin:0}button{background:none;border:1px solid var(--line);color:var(--text);border-radius:8px;padding:8px 12px;font:inherit}
main{padding:12px 16px;max-width:720px;margin:0 auto}.item{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:12px;margin:10px 0}
.name{font-weight:600;word-break:break-all}.meta{color:var(--dim);font-size:13px;margin:2px 0 10px}video{width:100%;border-radius:8px;background:#000;margin-bottom:10px}
a.dl{display:block;text-align:center;background:var(--accent);color:#06140d;text-decoration:none;font-weight:600;border-radius:8px;padding:10px}
.empty{color:var(--dim);text-align:center;padding:40px 0}
</style></head>
<body><header><h1>{{.Title}}</h1><button hx-get="/list" hx-target="#list">{{.Refresh}}</button></header>
<main id="list" hx-get="/list" hx-trigger="load, every 10s"></main></body></html>`))

var list = template.Must(template.New("list").Parse(`{{if not .Rows}}<p class="empty">{{.Text.Empty}}</p>{{end}}
{{range .Rows}}<div class="item"><div class="name">{{.Name}}</div><div class="meta">{{.Size}} · {{.When}}</div>
<video controls preload="none" src="/file/{{.N}}/{{.Name}}"></video>
<a class="dl" href="/file/{{.N}}/{{.Name}}?dl=1">{{.Download}}</a></div>{{end}}`))

// LANAddresses lists the private IPv4 addresses a phone on the same network can reach.
func LANAddresses() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}
