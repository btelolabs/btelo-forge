package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/btelolabs/btelo-forge/internal/ai"
	"github.com/btelolabs/btelo-forge/internal/gfx"
	"github.com/btelolabs/btelo-forge/internal/media"
	"github.com/btelolabs/btelo-forge/internal/proc"
	"github.com/btelolabs/btelo-forge/internal/vault"
)

const (
	icChat   = "" // chat
	icSend   = "" // send
	icMemory = "" // memory
	icCloud  = "" // cloud
	icKey    = "" // key
	icAI     = "" // smart_toy
	icThink  = "" // psychology
	icMoreV  = "" // expand_more
	icLessV  = "" // expand_less
	icAttach = "" // attach_file
	icPower  = "" // power_settings_new
)

// aiAnswerWake is how often a growing answer wakes the window: the text it holds is read at every frame, so a wake
// for each piece of it would only repeat itself.
const aiAnswerWake = 40 * time.Millisecond

// aiState is the AI models: the list on offer, the models in memory, the keys of online services, and the chat tab.
type aiState struct {
	config *ai.Config
	rt     *ai.Runtime
	keys   *ai.Keys
	chats  *ai.Chats

	keysErr error // why the saved keys could not be read; they are kept as they are until the user enters one

	list      []ai.ChatInfo // the conversations, the most recent first
	listed    bool
	chat      *ai.Chat // the conversation in the tab
	input     string
	answer    *aiAnswer
	picking   bool         // the list of models is open
	pickKind  ai.Kind      // the kind it shows, once one is clicked; until then that of the model in use
	pickTabs  []gfx.Rect   // where the kinds were last drawn, in the order of ai.ChatKinds
	opened    map[int]bool // the thinking of messages shown in full, by place in the conversation
	deleting  string       // the conversation whose deletion waits for confirmation
	notice    string       // what the last send could not do
	laid      map[string]*aiLaid
	orphans   []proc.Entry      // the model programs an earlier run left running
	orphansAt time.Time         // when they were last looked at
	pics      map[string]*aiPic // the pictures in the conversation, by file
	attached  []string          // the pictures that go with the message being written, by file
	scroll    [3]float32        // the messages, the conversations and the list of models
	content   [3]float32        // and how tall each is
	follow    bool              // the messages stay scrolled to the newest
	focusIn   bool              // the input takes the keyboard when next drawn
	voiceH    float32           // how tall the choice of voices was last drawn
	keyFor    string            // the service whose key is being entered in Settings
	keyText   string            // and what has been typed of it
	keyShown  map[string]bool   // services whose key was just saved, for a moment
	page      [2]float32        // the settings page's scroll and height
	removing  string            // the model whose files wait for confirmation to be deleted
}

// aiAnswer is an answer being written: the text arrives from another goroutine, so it is read under the lock.
type aiAnswer struct {
	mu       sync.Mutex
	stop     context.CancelFunc
	chat     *ai.Chat
	model    string // the id of the model answering
	text     string
	thinking string
	stats    ai.Stats
	err      error
	began    time.Time
	stage    string  // what a model of another part of the app is doing, and how far it has got
	fraction float64 // (see aiProgress)
	waiting  bool    // the model is being loaded before it can answer
	done     bool
	kind     ai.Kind    // what the model makes: words, or a picture
	left     int        // how many earlier messages were left out for the model to have room
	media    []ai.Media // what it made, kept in the assets folder
}

func (s *aiState) init(cacheDir, stateDir string, wake func()) {
	s.config = ai.DefaultConfig()
	// Every model program started from now on is written down, and those an earlier run left running are found.
	s.orphans, s.orphansAt = proc.SetRegistry(filepath.Join(stateDir, "running.json")), time.Now()
	s.rt = ai.NewRuntime(filepath.Join(cacheDir, "ai"), wake)
	s.chats = ai.NewChats(filepath.Join(stateDir, "chats"))
	s.keys, s.keysErr = ai.OpenKeys(filepath.Join(stateDir, "ai-keys.json"))
	s.opened, s.laid, s.keyShown = map[int]bool{}, map[string]*aiLaid{}, map[string]bool{}
	s.pics = map[string]*aiPic{}
	s.follow = true
}

// close stops the answer and unloads every model.
func (s *aiState) close() {
	if s.answer != nil {
		s.answer.stop()
	}
	s.rt.Close()
}

// busy reports whether an answer is being written.
func (s *aiState) busy() bool { return s.answer != nil }

// setConfig puts a configuration in effect: the models in memory may not be the ones it describes, so they are unloaded.
func (a *App) setAIConfig(c *ai.Config) {
	a.ai.config = c
	a.ai.rt.UnloadAll()
	a.ai.laid = map[string]*aiLaid{}
}

// aiChoice is the model the chat tab talks to: the conversation's, the user's last choice, or the default.
func (a *App) aiChoice() ai.Choice {
	for _, id := range []string{a.aiChatModel(), a.settings.AIModel, a.ai.config.DefaultModel} {
		if ch, ok := a.aiFind(id); ok {
			return ch
		}
	}
	return a.aiChoices()[0]
}

func (a *App) aiChatModel() string {
	if a.ai.chat != nil {
		return a.ai.chat.Model
	}
	return ""
}

// aiPick makes ch the model of the conversation and the user's choice for the next.
func (a *App) aiPick(ch ai.Choice) {
	a.settings.AIModel = ch.ID
	a.saveSettings()
	if a.ai.chat != nil {
		a.ai.chat.Model = ch.ID
	}
	a.ai.notice = ""
}

// aiEngine is the engine that runs a local model: the user's choice, or the first that runs on this computer.
func (a *App) aiEngine(m ai.Model) (ai.Engine, bool) {
	return a.ai.config.Engine(m, a.settings.AIEngine)
}

// aiLoad brings a local model into memory, unloading the others: a user switches models on a computer with memory
// for about one.
func (a *App) aiLoad(m ai.Model) {
	e, ok := a.aiEngine(m)
	if !ok {
		a.toast(a.u.tf("No engine for %s runs on this computer", m.Name.In(string(a.u.lang))), true)
		return
	}
	if err := a.ai.rt.Load(a.ai.config, m.ID, e.ID, true); err != nil {
		a.toast(a.describeAI(err), true)
	}
}

// aiKey is the API key of a service, and whether there is one.
func (a *App) aiKey(provider string) (string, bool) {
	key, err := a.ai.keys.Get(provider)
	if err != nil {
		if errors.Is(err, vault.ErrCannotOpen) {
			a.ai.notice = a.u.t("The saved key was made by another user or on another computer; enter it again")
		}
		return "", false
	}
	return key, key != ""
}

// describeAI turns a failure of a model into a message for the user.
func (a *App) describeAI(err error) string {
	u := &a.u
	switch {
	case errors.Is(err, ai.ErrNoKey):
		return u.t("Enter this service's API key in Settings → AI models first")
	case errors.Is(err, ai.ErrClosed):
		return u.t("The app is closing")
	}
	text := err.Error()
	switch {
	case errors.Is(err, ai.ErrTooLong):
		return u.t("This message is longer than the model can remember; shorten it, or pick a model that remembers more")
	case errors.Is(err, ai.ErrNeedsPicture):
		return u.t("Attach a picture for the model to work from")
	case errors.Is(err, ai.ErrTooLong):
		return u.t("This message is longer than the model can remember; shorten it, or pick a model that remembers more")
	case errors.Is(err, ai.ErrNeedsPicture):
		return u.t("Attach a picture for the model to work from")
	case strings.Contains(text, "exceeds the available context size"):
		return u.tf("The conversation is longer than the model remembers; start a new chat. (%s)", text)
	case strings.Contains(text, "did not accept the API key"):
		return u.t("The service did not accept the API key; check it in Settings → AI models")
	case strings.Contains(text, "the engine could not make the video"):
		return u.tf("The video could not be made. If a game or another program is using the graphics card, close it and try again. (%s)", text)
	}
	return a.describe(err)
}

// aiSend sends what is in the input to the chosen model.
func (a *App) aiSend() {
	s := &a.ai
	text := strings.TrimSpace(s.input)
	ch := a.aiChoice()
	// Words are needed, except to a model that recognises speech in a file, or one that reads pictures.
	if (text == "" && !(len(s.attached) > 0 && aiTakesFiles(ch))) || s.answer != nil {
		return
	}
	if len(s.attached) > 0 && !aiTakesFiles(ch) {
		s.notice = a.u.t("The files attached do not suit this model; pick another, or remove them")
		return
	}
	if ch.StartsFromPicture() && len(s.attached) > 1 {
		s.notice = a.u.t("This model works from one picture; remove the others")
		return
	}
	if ch.Kind == ai.KindImage && text == "" {
		s.notice = a.u.t("Say how the picture should change")
		return
	}
	if ch.Kind == ai.KindModel3D && len(s.attached) == 0 {
		s.notice = a.u.t("Attach a picture for the model to work from")
		return
	}
	for _, p := range s.attached {
		if !aiAccepts(ch, p) {
			s.notice = a.u.t("The files attached do not suit this model; pick another, or remove them")
			return
		}
	}
	if ch.Online() {
		if _, ok := a.aiKey(ch.Provider.ID); !ok {
			s.notice = a.u.t("Enter this service's API key in Settings → AI models first")
			a.openSettingsPage(settingsAIPage)
			return
		}
	}
	if s.chat == nil {
		s.chat = ai.NewChat(ch.ID)
	}
	s.chat.Model = ch.ID
	msg := ai.Message{Role: ai.User, Text: text}
	for _, p := range s.attached {
		msg.Attached = append(msg.Attached, ai.Media{Kind: aiFileKind(p), Path: p})
	}
	s.chat.Messages = append(s.chat.Messages, msg)
	s.attached = nil
	s.input, s.notice, s.follow = "", "", true
	a.u.beginEdit("ai/input", "", true, 0, 0)
	a.aiAsk(ch)
}

// aiAsk has the model answer the conversation, in the background.
func (a *App) aiAsk(ch ai.Choice) {
	s := &a.ai
	if ch.Task() != "" {
		a.aiRunTask(ch)
		return
	}
	switch ch.Kind {
	case ai.KindImage:
		a.aiMake(ch)
		return
	case ai.KindVoice:
		a.aiSpeak(ch)
		return
	case ai.KindTranscribe:
		a.aiTranscribe(ch)
		return
	}
	ctx, stop := context.WithCancel(a.life)
	ans := &aiAnswer{stop: stop, chat: s.chat, model: ch.ID, began: time.Now(), kind: ch.Kind}
	s.answer = ans
	history := append([]ai.Message(nil), s.chat.Messages...)
	cfg := s.config
	engine := a.settings.AIEngine
	wake := a.host.Wake
	a.background(func(context.Context) {
		defer stop()
		var left int
		if ch.Local != nil {
			var fits bool
			if history, left, fits = ai.Fit(history, ch.Local.ContextOf()); !fits {
				a.post(func() { a.aiFinish(ans, ai.Stats{}, ai.ErrTooLong) })
				return
			}
		}
		history, err := withPictures(history)
		var ep ai.Endpoint
		if err == nil {
			ep, err = a.aiEndpoint(ctx, cfg, ch, engine, ans)
		}
		ans.mu.Lock()
		ans.left = left
		ans.mu.Unlock()
		ans.mu.Lock()
		ans.left = left
		ans.mu.Unlock()
		var stats ai.Stats
		if err == nil {
			last := time.Now()
			stats, err = ep.Chat(ctx, history, ai.Options{}, func(c ai.Chunk) {
				ans.mu.Lock()
				ans.text += c.Text
				ans.thinking += c.Reasoning
				ans.mu.Unlock()
				if time.Since(last) > aiAnswerWake {
					last = time.Now()
					wake()
				}
			})
		}
		a.post(func() { a.aiFinish(ans, stats, err) })
	})
}

// withPictures is the conversation with the pictures the user attached read from their files, ready to send.
func withPictures(messages []ai.Message) ([]ai.Message, error) {
	out := append([]ai.Message(nil), messages...)
	for i, m := range out {
		out[i].Images = nil
		for _, at := range m.Attached {
			if at.Kind != ai.KindImage {
				continue
			}
			img, err := ai.LoadImage(at.Path)
			if err != nil {
				return nil, err
			}
			out[i].Images = append(out[i].Images, img)
		}
	}
	return out, nil
}

// aiMaxAttached is how many pictures one message takes.
const aiMaxAttached = 6

// aiAttach adds the pictures among paths to the message being written, and says so when some cannot be.
func (a *App) aiAttach(paths []string) {
	s := &a.ai
	ch := a.aiChoice()
	if !aiTakesFiles(ch) {
		a.toast(a.u.t("This model takes no pictures or files"), true)
		return
	}
	added := 0
	for _, p := range paths {
		switch {
		case !aiAccepts(ch, p):
			a.toast(a.u.tf("%s does not suit this model", filepath.Base(p)), true)
		case slices.Contains(s.attached, p):
		case len(s.attached) >= aiMaxAttached:
			a.toast(a.u.tf("A message takes %d pictures at most", aiMaxAttached), true)
			return
		default:
			s.attached = append(s.attached, p)
			added++
		}
	}
	if added > 0 {
		s.focusIn = true
	}
}

// aiDropped attaches the files dropped on the chat that suit the model; it reports whether it took the drop, which it
// does not when none does, so that files are opened as they always are.
func (a *App) aiDropped(paths []string) bool {
	ch := a.aiChoice()
	var files []string
	for _, p := range paths {
		if aiAccepts(ch, p) {
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return false
	}
	a.aiAttach(files)
	return true
}

// aiPaste attaches what the clipboard holds when it is pictures; it reports whether it was, so that the paste does
// not also put text into the input.
func (a *App) aiPaste() bool {
	ch := a.aiChoice()
	if !aiTakesFiles(ch) {
		return false
	}
	img, files, _ := a.host.Paste()
	var paths []string
	for _, f := range files {
		if aiAccepts(ch, f) {
			paths = append(paths, f)
		}
	}
	if img != nil && len(paths) == 0 && ch.ReadsPictures() {
		p, err := a.storeImage(img)
		if err != nil {
			a.toast(a.u.tf("The pasted image could not be saved: %v", err), true)
			return true
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return false
	}
	a.aiAttach(paths)
	return true
}

// aiAssetRoot is the folder models' work is kept in, in a folder for each kind of model.
func (a *App) aiAssetRoot() (string, error) { return a.categoryDir(categoryIntelligence) }

// aiMake has an image model draw what the last message asks for, in the background, and keeps the picture in the
// assets folder, where the conversation shows it from.
func (a *App) aiMake(ch ai.Choice) {
	s := &a.ai
	root, err := a.aiAssetRoot()
	if err != nil {
		s.notice = a.describe(err)
		return
	}
	msg, _ := a.aiLastUser()
	prompt := msg.Text
	ctx, stop := context.WithCancel(a.life)
	ans := &aiAnswer{stop: stop, chat: s.chat, model: ch.ID, began: time.Now(), kind: ch.Kind}
	s.answer = ans
	cfg, engine := s.config, a.settings.AIEngine
	var opt ai.ImageOptions
	if ch.Local != nil {
		opt = ai.ImageOptions{Size: ch.Local.ImageSize, Steps: ch.Local.Steps}
	}
	a.background(func(context.Context) {
		defer stop()
		ep, err := a.aiEndpoint(ctx, cfg, ch, engine, ans)
		var pic ai.Picture
		var source *ai.Image
		if err == nil {
			source, err = aiSourcePicture(msg)
		}
		if err == nil {
			if source != nil {
				pic, err = ep.Redraw(ctx, prompt, *source, opt)
			} else {
				pic, err = ep.Generate(ctx, prompt, opt)
			}
		}
		if err == nil {
			var path string
			if path, err = ai.SaveAsset(root, ch.Kind, prompt, pic.Ext(), pic.Data, time.Now()); err == nil {
				ans.mu.Lock()
				ans.media = []ai.Media{{Kind: ch.Kind, Path: path}}
				ans.mu.Unlock()
			}
		}
		a.post(func() { a.aiFinish(ans, ai.Stats{}, err) })
	})
}

// aiSourcePicture is the picture attached to a message, read from its file, or nil when there is none.
func aiSourcePicture(msg ai.Message) (*ai.Image, error) {
	for _, at := range msg.Attached {
		if at.Kind == ai.KindImage {
			img, err := ai.LoadImage(at.Path)
			if err != nil {
				return nil, err
			}
			return &img, nil
		}
	}
	return nil, nil
}

// aiRunTask has a model that takes minutes make what the last message asks for, in the background: a video, or a 3D
// model of the picture attached. Stopping it tells the service, and what it made is kept in the assets folder.
func (a *App) aiRunTask(ch ai.Choice) {
	s := &a.ai
	root, err := a.aiAssetRoot()
	if err != nil {
		s.notice = a.describe(err)
		return
	}
	msg, _ := a.aiLastUser()
	ctx, stop := context.WithCancel(a.life)
	ans := &aiAnswer{stop: stop, chat: s.chat, model: ch.ID, began: time.Now(), kind: ch.Kind, fraction: -1}
	s.answer = ans
	cfg, engine, wake := s.config, a.settings.AIEngine, a.host.Wake
	req := ai.TaskRequest{Prompt: msg.Text}
	if ch.Remote != nil {
		req.Version = ch.Remote.Version
	}
	if ch.Local != nil {
		req.Video = aiVideoOptions(*ch.Local)
	}
	a.background(func(context.Context) {
		defer stop()
		ep, err := a.aiEndpoint(ctx, cfg, ch, engine, ans)
		var made ai.TaskResult
		if err == nil {
			req.Image, err = aiSourcePicture(msg)
		}
		if err == nil {
			made, err = ep.RunTask(ctx, ch.Task(), req, func(fraction float64) {
				ans.mu.Lock()
				ans.fraction = fraction
				ans.mu.Unlock()
				wake()
			})
		}
		if err == nil {
			name := msg.Text
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(msg.Attached[0].Path), filepath.Ext(msg.Attached[0].Path))
			}
			var path string
			if path, err = ai.SaveAsset(root, ch.Kind, name, made.Ext, made.Data, time.Now()); err == nil {
				if ch.Local != nil {
					// The video is already made, and this app can play it as it is.
					media.OpusWebM(ctx, path)
				}
				ans.mu.Lock()
				ans.media = []ai.Media{{Kind: ch.Kind, Path: path}}
				ans.mu.Unlock()
			}
		}
		a.post(func() { a.aiFinish(ans, ai.Stats{}, err) })
	})
}

// aiVideoOptions is what a video model of this computer makes, from its entry in the list.
func aiVideoOptions(m ai.Model) ai.VideoOptions {
	v := ai.VideoOptions{Frames: m.Frames, FPS: m.FPS, Steps: m.Steps}
	fmt.Sscanf(m.ImageSize, "%dx%d", &v.Width, &v.Height)
	return v
}

// aiEndpoint is where to ask the model: an online service with the user's key, or the local model once it is loaded.
func (a *App) aiEndpoint(ctx context.Context, cfg *ai.Config, ch ai.Choice, engine string, ans *aiAnswer) (ai.Endpoint, error) {
	if ch.Online() {
		key, err := a.ai.keys.Get(ch.Provider.ID)
		if err != nil || key == "" {
			return ai.Endpoint{}, ai.ErrNoKey
		}
		return ai.Endpoint{URL: ch.Provider.BaseURL, Key: key, Model: ch.Remote.Model, Client: http.DefaultClient}, nil
	}
	e, ok := cfg.Engine(*ch.Local, engine)
	if !ok {
		return ai.Endpoint{}, errors.New("no engine for this model runs on this computer")
	}
	if _, ready := a.ai.rt.Endpoint(ch.ID); !ready {
		ans.mu.Lock()
		ans.waiting = true
		ans.mu.Unlock()
		if err := a.ai.rt.Load(cfg, ch.ID, e.ID, true); err != nil {
			return ai.Endpoint{}, err
		}
		if err := a.ai.rt.Wait(ctx, ch.ID); err != nil {
			return ai.Endpoint{}, err
		}
		ans.mu.Lock()
		ans.waiting = false
		ans.mu.Unlock()
	}
	ep, ok := a.ai.rt.Endpoint(ch.ID)
	if !ok {
		return ai.Endpoint{}, errors.New("the model was unloaded")
	}
	return ep, nil
}

// aiFinish keeps what was answered in the conversation and saves it. An answer stopped by the user is kept as far
// as it got.
func (a *App) aiFinish(ans *aiAnswer, stats ai.Stats, err error) {
	s := &a.ai
	ans.mu.Lock()
	ans.done, ans.stats, ans.err = true, stats, err
	text, thinking, media := ans.text, ans.thinking, ans.media
	ans.mu.Unlock()
	if s.answer == ans {
		s.answer = nil
	}
	chat := ans.chat
	stopped := errors.Is(err, context.Canceled)
	if err != nil && !stopped {
		s.notice = a.describeAI(err)
	}
	if text != "" || thinking != "" || len(media) > 0 {
		msg := ai.Message{Role: ai.Assistant, Text: text, Reasoning: thinking, Media: media, LeftOut: ans.left}
		if stats.CompletionTokens > 0 {
			msg.Stats = &stats
		}
		chat.Messages = append(chat.Messages, msg)
	}
	if text != "" || thinking != "" || len(media) > 0 || stopped {
		chat.Updated = time.Now()
		if err := s.chats.Save(chat); err != nil {
			a.toast(a.u.tf("The conversation could not be saved: %v", err), true)
		}
		s.listed = false
	}
}

// aiClosed is what closing the chat tab ends: the answer being written and every model it had running, so that nothing
// is left holding the computer's memory for a window that is gone.
func (a *App) aiClosed() {
	a.aiStop()
	a.ai.rt.UnloadAll()
	if a.speech.job == nil {
		a.speech.server.Stop()
	}
}

// aiStop ends the answer being written.
func (a *App) aiStop() {
	if a.ai.answer != nil {
		a.ai.answer.stop()
	}
}

// aiNewChat starts an empty conversation.
func (a *App) aiNewChat() {
	s := &a.ai
	a.aiStop()
	s.chat, s.notice, s.follow, s.attached = nil, "", true, nil
	s.opened = map[int]bool{}
	a.u.beginEdit("ai/input", "", true, 0, 0)
	s.input = ""
}

// aiOpenChat shows a saved conversation.
func (a *App) aiOpenChat(id string) {
	s := &a.ai
	chat, err := s.chats.Load(id)
	if err != nil {
		a.toast(a.u.tf("The conversation could not be opened: %v", err), true)
		return
	}
	a.aiStop()
	s.chat, s.notice, s.follow = chat, "", true
	s.opened = map[int]bool{}
	if chat.Model != "" {
		if ch, ok := a.aiFind(chat.Model); ok {
			a.settings.AIModel = ch.ID
		}
	}
}

// aiDeleteChat removes a saved conversation.
func (a *App) aiDeleteChat(id string) {
	s := &a.ai
	if err := s.chats.Delete(id); err != nil {
		a.toast(a.u.tf("The conversation could not be deleted: %v", err), true)
		return
	}
	if s.chat != nil && s.chat.ID == id {
		a.aiNewChat()
	}
	s.deleting, s.listed = "", false
}

// aiConversations is the list of saved conversations, read again after a change.
func (a *App) aiConversations() []ai.ChatInfo {
	s := &a.ai
	if !s.listed {
		list, err := s.chats.List()
		if err != nil {
			a.toast(a.u.tf("The conversations could not be listed: %v", err), true)
		}
		s.list, s.listed = list, true
	}
	return s.list
}

// speechPid is the process id of the voice-over engine while it runs, or 0, for the list of AI models to measure it.
func (a *App) speechPid() int {
	_, pid, _ := a.speech.server.Info()
	return pid
}

// animating reports whether something shows movement: an answer being written, or a model being loaded.
func (s *aiState) animating() bool {
	if s.answer != nil {
		return true
	}
	for _, st := range s.rt.Statuses() {
		if st.State == ai.Loading || st.State == ai.Downloading {
			return true
		}
	}
	return false
}

// openSettingsPage opens Settings on a page.
func (a *App) openSettingsPage(page int) { a.settingsOpen, a.settingsPage = true, page }
