package music

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed emoticons.json
var emoticonsJSON []byte

var commentEmoticons = sync.OnceValue(func() map[string]string {
	var raw map[string]string
	if err := json.Unmarshal(emoticonsJSON, &raw); err != nil {
		panic("music: invalid emoticons.json: " + err.Error())
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[strings.ToLower(k)] = v
	}
	return out
})

// maxEmoticonBytes bounds how far ahead a '[' scans for a matching ']'.
const maxEmoticonBytes = 24

// FormatCommentText converts common music-platform bracketed emoticon
// shortcodes (such as [rose]-style tokens from Kugou/NetEase/QQ comments) into
// standard Unicode emoji while leaving unrecognized bracketed text untouched.
func FormatCommentText(s string) string {
	if !strings.ContainsRune(s, '[') {
		return s
	}
	table := commentEmoticons()
	var b strings.Builder
	wrote := false
	pos := 0
	for i := 0; i < len(s); {
		if s[i] != '[' {
			i++
			continue
		}
		limit := min(len(s), i+1+maxEmoticonBytes)
		closeIdx := -1
		for j := i + 1; j < limit; j++ {
			if s[j] == '[' {
				break
			}
			if s[j] == ']' {
				closeIdx = j
				break
			}
		}
		if closeIdx > i+1 {
			key := strings.ToLower(s[i+1 : closeIdx])
			if emoji, ok := table[key]; ok {
				if !wrote {
					b.Grow(len(s))
					wrote = true
				}
				b.WriteString(s[pos:i])
				b.WriteString(emoji)
				pos = closeIdx + 1
				i = pos
				continue
			}
		}
		i++
	}
	if !wrote {
		return s
	}
	b.WriteString(s[pos:])
	return b.String()
}
