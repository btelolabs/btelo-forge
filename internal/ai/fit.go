package ai

import (
	"errors"
	"unicode"
)

// ErrTooLong means the newest message alone does not fit in what the model remembers.
var ErrTooLong = errors.New("the message is longer than the model remembers")

// imageTokens is what a picture is taken to cost; a model's own count is near this for the sizes LoadImage sends.
const imageTokens = 1100

// messageTokens is what a message's role and separators cost.
const messageTokens = 8

// EstimateTokens is a cautious count of the tokens text takes: the engines' counts differ by model, and a count too low
// makes the model refuse the conversation. A character of Chinese, Japanese or Korean is a token or more, and other
// text about one token to three characters.
func EstimateTokens(text string) int {
	wide, other := 0, 0
	for _, r := range text {
		if r > 0x2E7F && (unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)) {
			wide++
		} else {
			other++
		}
	}
	return wide*3/2 + (other+2)/3
}

func (m Message) tokens() int {
	n := messageTokens + EstimateTokens(m.Text)
	for _, at := range m.Attached {
		if at.Kind == KindImage {
			n += imageTokens
		}
	}
	return n
}

// Fit keeps the newest messages that leave a quarter of the model's memory for the answer, and returns them with how
// many were left out. It reports false when not even the last message fits. A conversation starts with what the user
// wrote, so what is kept does too, and a system message stays whatever is left out after it.
func Fit(messages []Message, context int) ([]Message, int, bool) {
	room := context * 3 / 4
	var system []Message
	rest := messages
	if len(rest) > 0 && rest[0].Role == System {
		system, rest = rest[:1], rest[1:]
		room -= system[0].tokens()
	}
	from, used := len(rest), 0
	for from > 0 {
		cost := rest[from-1].tokens()
		if used+cost > room {
			break
		}
		used += cost
		from--
	}
	if from == len(rest) && len(rest) > 0 {
		return nil, 0, false
	}
	for from < len(rest) && rest[from].Role != User {
		from++
	}
	if from == 0 {
		return messages, 0, true
	}
	return append(append([]Message(nil), system...), rest[from:]...), from, true
}
