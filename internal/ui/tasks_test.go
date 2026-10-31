package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/library"
	"github.com/btelolabs/btelo-forge/internal/project"
)

// slowTrack serves a track whose first half arrives at once and the rest only once release is closed.
func slowTrack(t *testing.T) (library.Track, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	body := strings.Repeat("x", 64<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "131072")
		w.Write([]byte(body))
		w.(http.Flusher).Flush()
		select {
		case <-release:
			w.Write([]byte(body))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		srv.Close()
	})
	return library.Track{Provider: "ia", ID: "slow", Title: "Slow Song", Duration: 2, URL: srv.URL + "/slow.mp3", Ext: ".mp3"}, release
}

func musicClips(p *project.Project) int {
	n := 0
	for _, t := range p.Music {
		n += len(t.Clips)
	}
	return n
}

func TestTaskCardShowsADownloadAndCancelsIt(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	edit := x.a.doc
	track, release := slowTrack(t)
	x.a.addTrack(track)
	x.pump(10*time.Second, func() bool { read, _, ok := x.a.lib.Progress(track); return ok && read > 0 })
	tasks := x.a.tasks()
	if len(tasks) != 1 || tasks[0].title != "Adding Slow Song" || tasks[0].status != "Downloading" || tasks[0].fraction != 0.5 {
		t.Fatalf("tasks %+v", tasks)
	}
	l := x.a.layoutTasks()
	cards, cancels := l.cards, l.cancels
	if c := cards[0]; c.Right() != x.a.screen.W-16 || c.Bottom() != x.a.screen.H-16 {
		t.Fatalf("the card is at %+v, not in the bottom right corner of %+v", c, x.a.screen)
	}
	x.shot("task-card")

	// Cancelling gives up adding the track, quietly; the file keeps downloading for next time.
	x.click(cancels[0])
	x.pump(10*time.Second, func() bool { return len(x.a.music.adding) == 0 })
	if musicClips(x.a.p) != 0 || slices.ContainsFunc(x.a.toasts, func(t toast) bool { return t.err }) {
		t.Fatalf("after cancelling: music %+v, toasts %+v", x.a.p.Music, x.a.toasts)
	}

	// Started again, it joins that download. Another tab is named on the card, and the card brings the edit back.
	x.a.addTrack(track)
	x.a.newTab()
	x.frame(Input{})
	if tasks := x.a.tasks(); len(tasks) != 1 || tasks[0].title != "Adding Slow Song · "+x.a.tabName(edit) {
		t.Fatalf("tasks %+v", tasks)
	}
	x.a.tab = tabExport
	x.click(cards[0])
	if x.a.doc != edit || x.a.tab != tabMusic {
		t.Fatal("the card did not bring its edit and panel forward")
	}

	// It lands in the edit it was added to, whichever tab is in front by then.
	x.a.newTab()
	other := x.a.doc
	close(release)
	x.pump(10*time.Second, func() bool { return len(x.a.music.adding) == 0 })
	if other.p != nil || musicClips(edit.p) != 1 || edit.p.Music[0].Clips[0].Title != "Slow Song" || !edit.hist.CanUndo() {
		t.Fatalf("the track went astray: %+v", edit.p.Music)
	}
	if len(x.a.tasks()) != 0 {
		t.Fatal("the card stayed after the work ended")
	}
}

func TestTaskCardsWaitWhileADialogHoldsTheWindow(t *testing.T) {
	x := newHarness(t)
	x.open(x.fixture())
	_, stop := context.WithCancel(context.Background())
	stopped := false
	x.a.speech.job = &speechJob{stop: func() { stopped = true; stop() }, doc: x.a.doc, stage: "load", lines: 1}
	x.a.subtitles.job = &subtitleJob{stop: stop, doc: x.a.doc, stage: "transcribe", fraction: 0.25}
	t.Cleanup(func() { x.a.speech.job, x.a.subtitles.job = nil, nil })
	x.frame(Input{})
	tasks := x.a.tasks()
	if len(tasks) != 2 || tasks[0].title != "Generating subtitles" || tasks[0].status != "Recognising speech" || tasks[0].fraction != 0.25 ||
		tasks[1].title != "Generating speech" || tasks[1].status != "Loading the model into memory" || tasks[1].fraction >= 0 {
		t.Fatalf("tasks %+v", tasks)
	}
	l := x.a.layoutTasks()
	cards, cancels := l.cards, l.cancels
	if cards[1].Bottom() > cards[0].Y {
		t.Fatal("the cards overlap")
	}
	x.a.u.lang, x.a.tab = Chinese, tabSpeech
	x.frame(Input{})
	x.shot("task-cards-zh")
	x.a.u.lang = English

	x.a.settingsOpen = true
	x.click(cancels[1])
	if stopped {
		t.Fatal("a card under the settings took the click")
	}
	x.a.settingsOpen = false
	x.frame(Input{})
	x.click(cancels[1])
	if !stopped || !x.a.speech.job.stopping {
		t.Fatal("the cancel button did not stop the speech")
	}
	if tasks := x.a.tasks(); !tasks[1].stopping {
		t.Fatal("the card does not show that it is stopping")
	}
}
