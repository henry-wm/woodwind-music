package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NSGo v0.3.0 ties Video playback and progress callbacks to the Android view.
// The packaged host opts out of the visibility pause for its service-owned
// view. Progress goes through the main looper even after the view detaches.
func stagePlaybackFrameworkSource(source, stage string) (string, error) {
	name := filepath.Base(source)
	type replacement struct {
		old, new string
		count    int
	}
	var edits []replacement
	switch name {
	case "NsgoView.java":
		edits = []replacement{
			{"    private void pauseVideos() {", `    protected boolean keepsVideosPlayingInBackground() { return false; }

    private void pauseVideos() {
        if (keepsVideosPlayingInBackground()) return;`, 1},
		}
	case "VideoNode.java":
		edits = []replacement{
			{"    private final Runnable progress = this::progress;", `    private final android.os.Handler progressHandler = new android.os.Handler(android.os.Looper.getMainLooper());
    private final Runnable progress = this::progress;`, 1},
			{"removeCallbacks(progress);", "progressHandler.removeCallbacks(progress);", 4},
			{"postDelayed(progress,", "progressHandler.postDelayed(progress,", 2},
			{"post(progress);", "progressHandler.post(progress);", 1},
		}
	default:
		return source, nil
	}

	raw, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	text := string(raw)
	for _, edit := range edits {
		if strings.Count(text, edit.old) != edit.count {
			return "", fmt.Errorf("NSGo Android playback patch no longer matches %s: %q", name, edit.old)
		}
		text = strings.ReplaceAll(text, edit.old, edit.new)
	}
	path := filepath.Join(stage, name)
	return path, os.WriteFile(path, []byte(text), 0600)
}
