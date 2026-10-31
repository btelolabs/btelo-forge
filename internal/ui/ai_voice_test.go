package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/speech"
	"github.com/btelolabs/btelo-forge/internal/transcribe"
)

func TestTheChatListsTheVoiceAndSpeechModelsUnderTheirKinds(t *testing.T) {
	x := newHarness(t)
	kinds := map[ai.Kind][]string{}
	for _, ch := range x.a.aiChoices() {
		kinds[ch.Kind] = append(kinds[ch.Kind], ch.ID)
	}
	for _, c := range x.a.speech.config.Models {
		id := ai.ExtID(ai.ExtSpeech, c.ID)
		ch, ok := x.a.aiFind(id)
		if !ok || ch.Kind != ai.KindVoice || !ch.External() || ch.Name.In("en") == "" {
			t.Errorf("%s: %+v %v", id, ch, ok)
		}
	}
	for _, c := range x.a.subtitles.config.Models {
		id := ai.ExtID(ai.ExtSubtitles, c.ID)
		if ch, ok := x.a.aiFind(id); !ok || ch.Kind != ai.KindTranscribe {
			t.Errorf("%s: %+v %v", id, ch, ok)
		}
	}
	if len(kinds[ai.KindVoice]) == 0 || len(kinds[ai.KindTranscribe]) == 0 || len(kinds[ai.KindLLM]) == 0 {
		t.Fatalf("kinds %v", kinds)
	}
	if _, ok := x.a.aiFind("speech/nope"); ok {
		t.Error("an unknown voice model exists")
	}
	x.a.openAITab()
	x.a.ai.picking = true
	x.a.ai.pickKind = ai.KindVoice
	x.frame(Input{})
	x.shot("ai-picker-voice")
}

func TestSoundAndVideoAreAttachedForSpeechRecognitionOnly(t *testing.T) {
	x := newHarness(t)
	dir := t.TempDir()
	wav, pic, text := filepath.Join(dir, "talk.wav"), filepath.Join(dir, "a.png"), filepath.Join(dir, "a.txt")
	os.WriteFile(wav, []byte("RIFF"), 0o644)
	os.WriteFile(pic, gradientPNG(t, 8, 8), 0o644)
	os.WriteFile(text, []byte("x"), 0o644)
	voice, _ := x.a.aiFind(ai.ExtID(ai.ExtSubtitles, x.a.subtitles.config.DefaultModel))
	x.a.aiPick(voice)
	x.a.openAITab()
	x.frame(Input{})
	x.a.aiAttach([]string{wav, pic, text})
	if got := x.a.ai.attached; len(got) != 1 || got[0] != wav {
		t.Fatalf("attached %v", got)
	}
	x.frame(Input{})
	x.shot("ai-stt-attached")
	// Sound alone is a message; words alone are not, to a model that recognises speech.
	x.a.ai.attached = nil
	x.a.ai.input = "hello"
	x.a.aiSend()
	if x.a.ai.answer != nil {
		t.Fatal("words were sent to a model that recognises speech")
	}
	// Sound is not for a model that only talks.
	chat, _ := x.a.ai.config.Choice(x.a.ai.config.DefaultModel)
	x.a.aiPick(chat)
	x.a.aiAttach([]string{wav})
	if len(x.a.ai.attached) != 0 {
		t.Fatal("sound was attached for a language model")
	}
}

func TestTranscriptIsALineToEachStretchWithItsTime(t *testing.T) {
	got := transcriptOf([]transcribe.Line{{Start: 0, Text: " Hello there. "}, {Start: 61.9, Text: "   "}, {Start: 75, Text: "Second."}})
	want := "`00:00`  Hello there.\n\n`01:15`  Second."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if transcriptOf(nil) != "" {
		t.Fatal("an empty transcript")
	}
}

// TestRealVoiceAndSpeechRecognitionInTheChat runs the real engines and models from FORGE_REAL_CACHE, the app's cache
// folder (it holds speech and whisper): a sentence is spoken, kept in the ai-voice folder, then written down again.
// It is skipped otherwise, as both are large downloads.
func TestRealVoiceAndSpeechRecognitionInTheChat(t *testing.T) {
	cache := os.Getenv("FORGE_REAL_CACHE")
	if cache == "" {
		t.Skip("FORGE_REAL_CACHE is not set")
	}
	x := newHarness(t)
	x.a.speech.manager = transcribe.NewManager(filepath.Join(cache, "speech"))
	x.a.speech.engines = filepath.Join(cache, "speech", "run")
	x.a.subtitles.manager = transcribe.NewManager(filepath.Join(cache, "whisper"))
	voiceID, sttID := x.a.speech.config.DefaultModel, x.a.subtitles.config.DefaultModel
	sm, _ := x.a.speech.config.Model(voiceID)
	if _, ok := x.a.speech.manager.ModelPath(sm.Model); !ok {
		t.Skip("the voice-over model is not downloaded")
	}
	tm, _ := x.a.subtitles.config.Model(sttID)
	if _, ok := x.a.subtitles.manager.ModelPath(tm); !ok {
		t.Skip("the speech recognition model is not downloaded")
	}
	root := t.TempDir()
	x.a.settings.ExportDir = root
	x.a.settings.SubtitleLanguage = "en"
	x.a.openAITab()
	x.frame(Input{})

	voice, _ := x.a.aiFind(ai.ExtID(ai.ExtSpeech, voiceID))
	x.a.aiPick(voice)
	x.typeText("Good morning. The weather is very nice today.")
	x.key(keyEnter, false)
	if x.a.ai.answer == nil {
		t.Fatalf("not sent: %q", x.a.ai.notice)
	}
	x.pump(8*time.Minute, x.answered)
	x.settle(5 * time.Second)
	msgs := x.a.ai.chat.Messages
	if x.a.ai.notice != "" || len(msgs) != 2 || len(msgs[1].Media) != 1 {
		t.Fatalf("notice %q, messages %+v", x.a.ai.notice, msgs)
	}
	wav := msgs[1].Media[0].Path
	if secs, ok := speech.Length(wav); !ok || secs < 1 {
		t.Fatalf("the recording %s is %v s (%v)", wav, secs, ok)
	}
	if !strings.Contains(filepath.ToSlash(wav), "/ai-voice/") {
		t.Fatalf("kept at %s", wav)
	}
	x.shot("ai-voice-made")

	stt, _ := x.a.aiFind(ai.ExtID(ai.ExtSubtitles, sttID))
	x.a.aiNewChat()
	x.a.aiPick(stt)
	x.a.aiAttach([]string{wav})
	x.a.aiSend()
	if x.a.ai.answer == nil {
		t.Fatalf("not sent: %q", x.a.ai.notice)
	}
	x.pump(5*time.Minute, x.answered)
	x.settle(5 * time.Second)
	got := x.a.ai.chat.Messages
	if x.a.ai.notice != "" || len(got) != 2 {
		t.Fatalf("notice %q, messages %+v", x.a.ai.notice, got)
	}
	if low := strings.ToLower(got[1].Text); !strings.Contains(low, "weather") && !strings.Contains(low, "morning") {
		t.Fatalf("the recording was written down as %q", got[1].Text)
	}
	x.shot("ai-voice-written")
}
