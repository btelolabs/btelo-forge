package win

import (
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// testRoot is a stand-in for HKEY_CURRENT_USER\Software of its own, removed after the test, so the test never
// touches the real associations.
func testRoot(t *testing.T) AssocRoot {
	base := fmt.Sprintf(`Software\BteloForgeTest-%d-%d`, os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { deleteTree(base) })
	return AssocRoot{Software: base, App: `BteloLabs\BteloForge`, Name: "Btelo Forge", Exe: `C:\Apps\Btelo Forge\btelo-forge.exe`}
}

func TestAssociationsRegisterAndGoAway(t *testing.T) {
	r := testRoot(t)
	video := Association{ProgID: "BteloForge.Video", Name: "Video", Exts: []string{".mp4", ".mov"}}
	canvas := Association{ProgID: "BteloForge.Canvas", Name: "Canvas", Exts: []string{".btcanvas"}, Own: true}
	if err := Register(r, []Association{video, canvas}, "Edit videos"); err != nil {
		t.Fatal(err)
	}
	if !Registered(r, video) || !Registered(r, canvas) {
		t.Fatal("not registered")
	}
	if got := getString(r.classes()+`\BteloForge.Video\shell\open\command`, ""); got != `"C:\Apps\Btelo Forge\btelo-forge.exe" "%1"` {
		t.Fatalf("command %q", got)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, r.classes()+`\.mp4\OpenWithProgids`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := k.ReadValueNames(-1)
	k.Close()
	if len(names) != 1 || names[0] != "BteloForge.Video" {
		t.Fatalf("open with %v", names)
	}
	// The app's own format becomes the default; a common one only joins Open with.
	if !DefaultFor(r, ".btcanvas", canvas.ProgID) || DefaultFor(r, ".mp4", video.ProgID) {
		t.Fatal("defaults")
	}
	if getString(r.Software+`\RegisteredApplications`, "Btelo Forge") != `BteloLabs\BteloForge\Capabilities` {
		t.Fatal("not in RegisteredApplications")
	}
	// A moved program is not registered until it registers again.
	moved := r
	moved.Exe = `D:\btelo-forge.exe`
	if Registered(moved, video) {
		t.Fatal("a moved program reads as registered")
	}

	if err := Unregister(r, []Association{video}); err != nil {
		t.Fatal(err)
	}
	if Registered(r, video) || !Registered(r, canvas) {
		t.Fatal("unregistering one took the wrong one")
	}
	if err := Unregister(r, []Association{canvas}); err != nil {
		t.Fatal(err)
	}
	if getString(r.classes()+`\.btcanvas`, "") != "" || getString(r.Software+`\RegisteredApplications`, "Btelo Forge") != "" {
		t.Fatal("the declaration outlived the last association")
	}
}
