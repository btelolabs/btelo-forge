package ai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Chat is a conversation kept on this computer.
type Chat struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Model    string    `json:"model"` // the model it was last held with: a Choice's id
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Messages []Message `json:"messages"`
}

// ChatInfo is a conversation without its messages, for a list.
type ChatInfo struct {
	ID      string
	Title   string
	Model   string
	Updated time.Time
}

// Chats keeps conversations as files in a folder, one each.
type Chats struct{ dir string }

// NewChats keeps conversations in dir.
func NewChats(dir string) *Chats { return &Chats{dir: dir} }

var chatID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// NewChat is an empty conversation held with model.
func NewChat(model string) *Chat {
	b := make([]byte, 8)
	rand.Read(b)
	now := time.Now()
	return &Chat{ID: hex.EncodeToString(b), Model: model, Created: now, Updated: now}
}

func (c *Chats) path(id string) (string, error) {
	if !chatID.MatchString(id) {
		return "", fmt.Errorf("%q is not a conversation", id)
	}
	return filepath.Join(c.dir, id+".json"), nil
}

// Save writes the conversation. A conversation with no messages is not kept, so opening a new one and leaving it
// leaves nothing behind.
func (c *Chats) Save(chat *Chat) error {
	p, err := c.path(chat.ID)
	if err != nil {
		return err
	}
	if len(chat.Messages) == 0 {
		return nil
	}
	if chat.Title == "" {
		chat.Title = TitleOf(chat.Messages[0].Text)
	}
	data, err := json.Marshal(chat)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load reads a conversation.
func (c *Chats) Load(id string) (*Chat, error) {
	p, err := c.path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var chat Chat
	if err := json.Unmarshal(data, &chat); err != nil {
		return nil, fmt.Errorf("conversation %s: %w", id, err)
	}
	chat.ID = id
	return &chat, nil
}

// Delete removes a conversation.
func (c *Chats) Delete(id string) error {
	p, err := c.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// List is the conversations, the most recently held first. A file that cannot be read is left out of the list, not
// deleted.
func (c *Chats) List() ([]ChatInfo, error) {
	entries, err := os.ReadDir(c.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ChatInfo
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !chatID.MatchString(id) {
			continue
		}
		chat, err := c.Load(id)
		if err != nil {
			continue
		}
		out = append(out, ChatInfo{ID: id, Title: chat.Title, Model: chat.Model, Updated: chat.Updated})
	}
	slices.SortFunc(out, func(a, b ChatInfo) int { return b.Updated.Compare(a.Updated) })
	return out, nil
}

// TitleOf is a conversation's name from its first message: its first line, cut at a length that fits a list.
func TitleOf(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.Join(strings.Fields(line), " ")
	const limit = 40
	if utf8.RuneCountInString(line) <= limit {
		return line
	}
	return string([]rune(line)[:limit-1]) + "…"
}
