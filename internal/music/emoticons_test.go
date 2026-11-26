package music

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatCommentText(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain comment", "plain comment"},
		{"送你一朵[玫瑰]", "送你一朵🌹"},
		{"[大哭][玫瑰][爱心][赞]", "😭🌹❤️👍"},
		{"[OK] 没问题 [Doge]", "👌 没问题 🐶"},
		{"[[玫瑰]]", "[🌹]"},
		{"[未闭合 [玫瑰]", "[未闭合 🌹"},
		{"[未知标签] 和 [] 保持原样", "[未知标签] 和 [] 保持原样"},
		{"已经带🌹和[微笑]", "已经带🌹和🙂"},
	}
	for _, tc := range cases {
		if got := FormatCommentText(tc.in); got != tc.want {
			t.Errorf("FormatCommentText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEmoticonsJSONHasUniqueNonEmptyEntries(t *testing.T) {
	dec := json.NewDecoder(strings.NewReader(string(emoticonsJSON)))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		t.Fatalf("expected object start, got %v, %v", tok, err)
	}
	seen := map[string]bool{}
	for dec.More() {
		kTok, err := dec.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		key := kTok.(string)
		var val string
		if err := dec.Decode(&val); err != nil {
			t.Fatalf("read value for %q: %v", key, err)
		}
		lower := strings.ToLower(key)
		if key == "" || val == "" {
			t.Errorf("empty entry: %q -> %q", key, val)
		}
		if seen[lower] {
			t.Errorf("duplicate emoticon key %q", key)
		}
		seen[lower] = true
	}
	if len(seen) < 50 {
		t.Fatalf("expected a comprehensive emoticon table, got %d entries", len(seen))
	}
}
