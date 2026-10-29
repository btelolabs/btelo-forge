package ui

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/update"
)

// releaseServer serves a release called tag the way GitHub does, its file holding the app as the release build
// packs it for this system; corrupt makes the file differ from its checksum.
type releaseServer struct {
	srv     *httptest.Server
	tag     atomic.Value
	corrupt atomic.Bool
	asked   atomic.Int32
}

func newReleaseServer(t *testing.T, tag string) *releaseServer {
	t.Helper()
	name := update.AssetName(runtime.GOOS, runtime.GOARCH)
	if name == "" {
		t.Skipf("no release is built for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	files := map[string]string{"Btelo Forge/LICENSE": "new licence", "Btelo Forge/Btelo Forge.exe": "new program"}
	if runtime.GOOS == "darwin" {
		files = map[string]string{"Btelo Forge/LICENSE": "new licence", "Btelo Forge/Btelo Forge.app/Contents/MacOS/btelo-forge": "new program",
			"Btelo Forge/Btelo Forge.app/Contents/Info.plist": "plist"}
	}
	for n, body := range files {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	z.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	rs := &releaseServer{}
	rs.tag.Store(tag)
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/file" {
			data := archive
			if rs.corrupt.Load() {
				data = slices.Clone(archive)
				data[len(data)/2] ^= 0xff
			}
			w.Write(data)
			return
		}
		rs.asked.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": rs.tag.Load(), "html_url": rs.srv.URL + "/page",
			"assets": []map[string]any{{"name": name, "browser_download_url": rs.srv.URL + "/file", "size": len(archive),
				"digest": "sha256:" + hex.EncodeToString(sum[:])}},
		})
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

// fakeInstall lays out an installed copy of the app as a release unpacks it on this system, and returns the program
// an update replaces.
func fakeInstall(t *testing.T) (exe string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Btelo Forge")
	exe = filepath.Join(dir, "Btelo Forge.exe")
	if runtime.GOOS == "darwin" {
		exe = filepath.Join(dir, "Btelo Forge.app", "Contents", "MacOS", "btelo-forge")
	}
	for p, body := range map[string]string{exe: "old program", filepath.Join(dir, "LICENSE"): "old licence"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return exe
}

// asRelease makes the app under test the release called tag, asking srv for releases.
func asRelease(t *testing.T, x *harness, tag string, rs *releaseServer) {
	t.Helper()
	oldVersion, oldURL := Version, releasesURL
	Version, releasesURL = tag, rs.srv.URL+"/latest"
	t.Cleanup(func() { Version, releasesURL = oldVersion, oldURL })
	x.a.upd.client = rs.srv.Client()
}

// clickToastAction clicks the button labelled label on the newest toast.
func (x *harness) clickToastAction(label string) {
	x.t.Helper()
	x.frame(Input{})
	_, buttons := x.a.toastLayout()
	for i := len(x.a.toasts) - 1; i >= 0; i-- {
		for k, act := range x.a.toasts[i].actions {
			if act.label == label {
				x.click(buttons[i][k])
				return
			}
		}
	}
	x.t.Fatalf("no toast offers %q: %v", label, x.a.toasts)
}

func (x *harness) toastSays(part string) bool {
	for _, t := range x.a.toasts {
		if strings.Contains(t.text, part) {
			return true
		}
	}
	return false
}

// A release build checks once a day on its own; a newer release is offered, and once the user agrees it is
// downloaded, checked and put in place of the installed copy, which is kept aside, and the app closes to start it.
func TestUpdateDailyCheckInstallsAndRestarts(t *testing.T) {
	x := newHarness(t)
	a := x.a
	rs := newReleaseServer(t, "v0.4.0")
	asRelease(t, x, "v0.3.1", rs)
	exe := fakeInstall(t)
	a.upd.exe = exe
	a.settings.Updates = updateSettings{Daily: true}

	x.pump(10*time.Second, func() bool { return a.newerRelease() != nil })
	if rs.asked.Load() != 1 || a.settings.Updates.Checked.IsZero() {
		t.Fatalf("asked %d times, checked at %v", rs.asked.Load(), a.settings.Updates.Checked)
	}
	if !x.toastSays("v0.4.0") || !a.updateWaiting() {
		t.Fatal("the newer release was not offered")
	}
	// Checked: the next check is a day away, however many frames are drawn.
	for range 5 {
		x.frame(Input{})
	}
	if due, wait := a.updateDue(); due || wait < updateInterval-time.Minute {
		t.Errorf("the next check is due %v, in %v", due, wait)
	}
	if rs.asked.Load() != 1 {
		t.Errorf("asked %d times in a day", rs.asked.Load())
	}
	a.openAbout()
	x.frame(Input{})
	x.shot(string(a.u.lang) + "-settings-update-available")
	if os.Getenv("FORGE_SHOTS") != "" {
		lang := a.u.lang
		a.u.lang = Chinese
		x.frame(Input{})
		x.shot("zh-settings-update-available")
		a.u.lang = lang
	}
	a.settingsOpen = false

	x.clickToastAction(a.u.t("Update and restart"))
	x.pump(10*time.Second, func() bool { return a.Quitting() })
	if a.upd.ready != "v0.4.0" || a.settings.Updates.Pending != "v0.4.0" {
		t.Fatalf("ready %q, pending %q", a.upd.ready, a.settings.Updates.Pending)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new program" {
		t.Fatalf("the program holds %q", got)
	}
	// What was replaced is kept aside until the next start: the program on Windows, the app bundle on macOS.
	launch, old := exe, exe+".update-old"
	if runtime.GOOS == "darwin" {
		launch = filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		old = filepath.Join(launch+".update-old", "Contents", "MacOS", "btelo-forge")
	}
	if got, _ := os.ReadFile(old); string(got) != "old program" {
		t.Fatalf("the replaced program was not kept aside: %q", got)
	}
	if a.upd.target.Launch != launch {
		t.Errorf("the app would start %q, want %q", a.upd.target.Launch, launch)
	}
	if left, _ := os.ReadDir(filepath.Join(a.cacheDir, "update")); len(left) > 0 {
		t.Errorf("the download was left in the cache: %v", left)
	}

	// The next start reports the update once.
	Version = "v0.4.0"
	a.toasts = nil
	a.updateStarted()
	if !x.toastSays("v0.4.0") || a.settings.Updates.Pending != "" {
		t.Fatalf("the start after the update said %v, pending %q", a.toasts, a.settings.Updates.Pending)
	}
}

// A download that is not the file the release published changes nothing; asked by hand, a check says when this is
// the newest version; a development build never checks by itself.
func TestUpdateRefusesABadDownload(t *testing.T) {
	x := newHarness(t)
	a := x.a
	rs := newReleaseServer(t, "v0.4.0")
	rs.corrupt.Store(true)
	asRelease(t, x, "v0.3.1", rs)
	exe := fakeInstall(t)
	a.upd.exe = exe
	a.settings.Updates = updateSettings{Daily: false}

	for range 5 {
		x.frame(Input{})
	}
	if rs.asked.Load() != 0 {
		t.Fatal("the app checked with the daily check turned off")
	}
	a.checkUpdate(true) // Settings → About → Check now
	x.pump(10*time.Second, func() bool { return a.newerRelease() != nil })
	x.clickToastAction(a.u.t("Update and restart"))
	x.pump(10*time.Second, func() bool {
		return a.upd.job == nil && x.toastSays(a.u.t("The download is not the file the release published; nothing was changed"))
	})
	if a.Quitting() || a.upd.ready != "" {
		t.Fatal("a bad download was installed")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old program" {
		t.Fatalf("a bad download changed the program: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "*.update-*")); len(left) > 0 {
		t.Errorf("a bad download left %v", left)
	}

	rs.tag.Store("v0.3.1")
	a.toasts = nil
	a.checkUpdate(true)
	x.pump(10*time.Second, func() bool { return x.toastSays(a.u.tf("%s %s is the newest version", ProductName, "v0.3.1")) })
	if a.updateWaiting() {
		t.Error("the same version is offered as newer")
	}

	Version = "dev"
	a.settings.Updates = updateSettings{Daily: true}
	asked := rs.asked.Load()
	for range 5 {
		x.frame(Input{})
	}
	if rs.asked.Load() != asked {
		t.Error("a development build checked by itself")
	}
}
