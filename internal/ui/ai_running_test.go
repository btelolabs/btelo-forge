package ui

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/proc"
	"github.com/btelolabs/btelo-forge/internal/speech"
)

// taskNamed is the model program with id among those running, wherever the window shows it.
func taskNamed(a *App, id string) (task, bool) {
	for _, t := range a.aiRunning() {
		if t.id == id {
			return t, true
		}
	}
	return task{}, false
}

func TestAModelRunningForTheChatShowsAsRunningAndStopsFromItsCardAndWhenTheTabCloses(t *testing.T) {
	x := newHarness(t)
	fakeSpeech(t, x)
	x.a.openAITab()
	voice, ok := x.a.aiFind(ai.ExtID(ai.ExtSpeech, "q8_0"))
	if !ok {
		t.Fatal("no voice model")
	}
	x.a.aiPick(voice)
	x.frame(Input{})
	if _, shown := taskNamed(x.a, "speech-engine"); shown {
		t.Fatal("a card shows before anything runs")
	}
	x.typeText("Hello.")
	x.key(keyEnter, false)
	x.pump(time.Minute, x.answered)
	x.settle(3 * time.Second)
	msgs := x.a.ai.chat.Messages
	if x.a.ai.notice != "" || len(msgs) != 2 || len(msgs[1].Media) != 1 {
		t.Fatalf("notice %q, messages %+v", x.a.ai.notice, msgs)
	}
	path := msgs[1].Media[0].Path
	root, _ := x.a.aiAssetRoot()
	if filepath.Dir(path) != filepath.Join(root, "ai-voice") {
		t.Fatalf("the recording is at %s, not in %s", path, filepath.Join(root, "ai-voice"))
	}
	if secs, ok := speech.Length(path); !ok || secs != 1.5 {
		t.Fatalf("the recording is %v s (%v)", secs, ok)
	}
	// The engine stays in memory after speaking, and says so.
	card, shown := taskNamed(x.a, "speech-engine")
	if !shown || !card.running || card.title != "Q8_0" || !strings.HasPrefix(card.status, "Running") || card.cancel == nil {
		t.Fatalf("the running engine has no card: %+v (%v)", card, shown)
	}
	x.frame(Input{})
	x.shot("ai-voice-running")
	// With the chat in front it is listed in the chat, not as a card over its input; on any other tab it is a card.
	if cardShown(x.a, "speech-engine") {
		t.Fatal("a card sits over the chat's input")
	}
	x.a.newTab()
	x.frame(Input{})
	if !cardShown(x.a, "speech-engine") {
		t.Fatal("no card on another tab")
	}
	x.shot("ai-voice-card-elsewhere")
	for i, d := range x.a.docs {
		if d.aiTab {
			x.a.switchTab(i)
		}
	}
	x.frame(Input{})
	_, pid, _ := x.a.speech.server.Info()
	if pid == 0 || !proc.Alive(pid) {
		t.Fatalf("the engine's process %d is not running", pid)
	}
	// The card stops it.
	card.cancel()
	x.pump(10*time.Second, func() bool { return !proc.Alive(pid) })
	if proc.Alive(pid) {
		t.Fatal("the engine runs after its card stopped it")
	}
	if _, shown := taskNamed(x.a, "speech-engine"); shown {
		t.Fatal("the card stayed after the engine stopped")
	}

	// Closing the chat tab stops what it started.
	x.a.aiNewChat()
	x.typeText("Again please.")
	x.key(keyEnter, false)
	x.pump(time.Minute, x.answered)
	_, pid, ok = x.a.speech.server.Info()
	if !ok || !proc.Alive(pid) {
		t.Fatal("the engine did not start again")
	}
	for i, d := range x.a.docs {
		if d.aiTab {
			x.a.closeTab(i)
			break
		}
	}
	x.pump(10*time.Second, func() bool { return !proc.Alive(pid) })
	if proc.Alive(pid) {
		t.Fatal("the engine runs after the chat tab was closed")
	}
}

// cardShown reports whether the window shows a card in its corner with the id.
func cardShown(a *App, id string) bool {
	for _, t := range a.tasks() {
		if t.id == id {
			return true
		}
	}
	return false
}

// leftRunning starts the test binary as a model server, outside the app, and writes it in the file the app keeps of
// what it runs, as an earlier run that crashed would have left it; then the app reads that file as it starts.
func leftRunning(t *testing.T, x *harness, label string) (*exec.Cmd, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "left.gguf", "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(), "SPEECH_FAKE_SERVER=1", "SPEECH_FAKE_LOG="+filepath.Join(t.TempDir(), "log"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	data, _ := json.Marshal([]proc.Entry{{Pid: cmd.Process.Pid, Program: exe, Label: label, Started: time.Now()}})
	file := filepath.Join(x.dir, "state", "running.json")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	x.a.ai.orphans, x.a.ai.orphansAt = proc.SetRegistry(file), time.Now()
	t.Cleanup(func() { proc.SetRegistry("") })
	return cmd, cmd.Process.Pid
}

func TestAModelAnEarlierRunLeftRunningIsShownAndCanBeStopped(t *testing.T) {
	x := newHarness(t)
	_, pid := leftRunning(t, x, "model:qwen3-4b")
	if len(x.a.ai.orphans) != 1 {
		t.Fatalf("orphans %+v", x.a.ai.orphans)
	}
	card, shown := taskNamed(x.a, "orphan/"+strconv.Itoa(pid))
	if !shown || !card.running || card.title != "Qwen3 4B" || card.status != "Left running by an earlier run" {
		t.Fatalf("the card: %+v (%v)", card, shown)
	}
	x.a.openAITab()
	x.frame(Input{})
	x.shot("ai-orphan-card")
	// It is in the list of Settings as running, with the way to stop it.
	installed, _ := x.a.aiRows()
	var row *aiRow
	for i := range installed {
		if installed[i].key == "orphan/"+strconv.Itoa(pid) {
			row = &installed[i]
		}
	}
	if row == nil || row.state != ai.Ready || row.unload == nil || row.name != "Qwen3 4B" {
		t.Fatalf("the list: %+v", installed)
	}
	x.a.openSettingsPage(settingsAIPage)
	x.frame(Input{})
	x.shot("ai-orphan-settings")
	x.a.settingsOpen = false

	card.cancel()
	x.pump(10*time.Second, func() bool { return !proc.Alive(pid) })
	if proc.Alive(pid) {
		t.Fatal("the program an earlier run left is still running")
	}
	x.frame(Input{})
	if _, shown := taskNamed(x.a, "orphan/"+strconv.Itoa(pid)); shown || len(x.a.ai.orphans) != 0 {
		t.Fatalf("it is still shown: %+v", x.a.ai.orphans)
	}
}

func TestAnOrphanThatEndedByItselfIsNotShownForLong(t *testing.T) {
	x := newHarness(t)
	cmd, pid := leftRunning(t, x, "speech:q8_0")
	if _, shown := taskNamed(x.a, "orphan/"+strconv.Itoa(pid)); !shown {
		t.Fatal("not shown")
	}
	cmd.Process.Kill()
	cmd.Wait()
	x.a.ai.orphansAt = time.Now().Add(-time.Minute) // the next look
	if _, shown := taskNamed(x.a, "orphan/"+strconv.Itoa(pid)); shown {
		t.Fatal("a program that has ended is shown as running")
	}
}
