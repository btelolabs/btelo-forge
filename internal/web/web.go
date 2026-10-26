// Package web is the page phones on the local network open: it lists the exported videos to play or download, and
// takes videos, sound and pictures uploaded from the phone. Everything is behind a random key in the address.
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
	"strings"
	"sync"
	"time"
)

//go:embed static/htmx.min.js
var htmx []byte

// File is one exported video.
type File struct {
	Path string
	At   time.Time
}

// Text is the page's wording, in the app's language. Uploaded and Refused are format strings taking a count and a
// list of names.
type Text struct {
	Title, Empty, Download, Refresh   string
	Exports, Upload, UploadHint, Send string
	Uploaded, Refused, Failed         string
}

// Site is what the page shows and where uploads go; its functions are called from server goroutines.
type Site struct {
	Files     func() []File
	Text      func() Text
	UploadDir func() (string, error)
	Received  func(paths []string)
	Logo      []byte // a JPEG of the app's mark, shown in the page header and as its icon
}

// Handler serves site to anyone presenting key.
func Handler(key string, site Site) http.Handler {
	files, text := site.Files, site.Text
	var storing sync.Mutex
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
	mux.HandleFunc("GET /logo.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(site.Logo)
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
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		t := text()
		dir, err := site.UploadDir()
		if err != nil {
			uploadResult.Execute(w, resultView{Failed: fmt.Sprintf(t.Failed, err)})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
		storing.Lock()
		saved, refused, err := receive(r, dir)
		storing.Unlock()
		if len(saved) > 0 {
			site.Received(saved)
		}
		v := resultView{}
		if len(saved) > 0 {
			names := make([]string, len(saved))
			for i, p := range saved {
				names[i] = filepath.Base(p)
			}
			v.Uploaded = fmt.Sprintf(t.Uploaded, len(saved), strings.Join(names, ", "))
		}
		if len(refused) > 0 {
			v.Refused = fmt.Sprintf(t.Refused, len(refused), strings.Join(refused, ", "))
		}
		if err != nil {
			v.Failed = fmt.Sprintf(t.Failed, err)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		uploadResult.Execute(w, v)
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
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover"><link rel="icon" href="/logo.jpg"><link rel="apple-touch-icon" href="/logo.jpg">
<title>{{.Title}}</title><script src="/htmx.min.js"></script>
<style>
:root{color-scheme:dark;--bg:#0f1112;--panel:#171a1c;--line:#2b3034;--text:#eceff0;--dim:#9ba3a8;--accent:#1fbf75}
*{box-sizing:border-box}html,button,input,a{touch-action:manipulation}body{-webkit-text-size-adjust:100%;margin:0;background:var(--bg);color:var(--text);font:16px/1.45 system-ui,-apple-system,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}
header{display:flex;align-items:center;justify-content:space-between;padding:18px 16px;border-bottom:1px solid var(--line)}
h1{font-size:18px;margin:0}.brand{display:flex;align-items:center;gap:10px;min-width:0}.brand img{width:32px;height:32px;border-radius:18%;flex:none}
button{background:none;border:1px solid var(--line);color:var(--text);border-radius:8px;padding:8px 12px;font:inherit;cursor:pointer}
.refresh.flash{animation:flash .6s}@keyframes flash{from{background:var(--accent);border-color:var(--accent)}to{background:none}}
main{padding:12px 16px;max-width:720px;margin:0 auto}.item{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:12px;margin:10px 0}
.name{font-weight:600;word-break:break-all}.meta{color:var(--dim);font-size:13px;margin:2px 0 10px}video{width:100%;border-radius:8px;background:#000;margin-bottom:10px}
a.dl{display:block;text-align:center;background:var(--accent);color:#fff;text-decoration:none;font-weight:600;border-radius:8px;padding:10px}
.empty{color:var(--dim);text-align:center;padding:40px 0}
h2{font-size:15px;color:var(--dim);margin:22px 0 4px;font-weight:600}
.upload{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:14px;margin:12px 0}
.upload p{color:var(--dim);font-size:13px;margin:0 0 10px}
input[type=file]{width:100%;color:var(--dim);margin-bottom:10px}
.send{width:100%;background:var(--accent);border:0;color:#fff;font-weight:600;padding:10px}
progress{width:100%;height:6px;margin-top:10px;accent-color:var(--accent)}progress[hidden]{display:none}
.ok{color:var(--accent);margin-top:10px}.bad{color:#ffb4b2;margin-top:10px}
</style></head>
<body><header><div class="brand"><img src="/logo.jpg" alt=""><h1>{{.Title}}</h1></div><button class="refresh" type="button" hx-get="/list" hx-target="#list" hx-on::after-request="this.classList.remove('flash');void this.offsetWidth;this.classList.add('flash')">{{.Refresh}}</button></header>
<main>
<form class="upload" hx-post="/upload" hx-encoding="multipart/form-data" hx-target="#sent" hx-on::before-request="var p=document.getElementById('progress');p.value=0;p.hidden=false" hx-on::after-request="this.reset();document.getElementById('progress').hidden=true;htmx.trigger('#list','refresh')">
<h1 style="font-size:16px;margin:0 0 4px">{{.Upload}}</h1><p>{{.UploadHint}}</p>
<input type="file" name="files" multiple accept="video/*,audio/*,image/*,image/heic,image/heif,video/quicktime,.heic,.heif,.mov" required>
<button class="send" type="submit">{{.Send}}</button>
<progress id="progress" max="100" value="0" hidden></progress><div id="sent"></div>
</form>
<h2>{{.Exports}}</h2>
<div id="list" hx-get="/list" hx-trigger="load, every 10s, refresh"></div>
</main>
<script>
var list=document.getElementById("list");
// A refresh that changes nothing, or arrives while a video plays, must not rebuild the players.
list.addEventListener("htmx:beforeSwap",function(e){
var playing=Array.prototype.some.call(list.querySelectorAll("video"),function(v){return !v.paused&&!v.ended});
if(e.detail.serverResponse===list.dataset.html||playing){e.detail.shouldSwap=false}else{list.dataset.html=e.detail.serverResponse}});
htmx.on("htmx:xhr:progress",function(e){if(e.detail.lengthComputable){document.getElementById("progress").value=e.detail.loaded/e.detail.total*100}})</script>
</body></html>`))

type resultView struct{ Uploaded, Refused, Failed string }

var uploadResult = template.Must(template.New("result").Parse(`{{if .Uploaded}}<div class="ok">{{.Uploaded}}</div>{{end}}` +
	`{{if .Refused}}<div class="bad">{{.Refused}}</div>{{end}}{{if .Failed}}<div class="bad">{{.Failed}}</div>{{end}}`))

var list = template.Must(template.New("list").Parse(`{{if not .Rows}}<p class="empty">{{.Text.Empty}}</p>{{end}}
{{range .Rows}}<div class="item"><div class="name">{{.Name}}</div><div class="meta">{{.Size}} · {{.When}}</div>
<video controls playsinline preload="metadata" src="/file/{{.N}}/{{.Name}}"></video>
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
