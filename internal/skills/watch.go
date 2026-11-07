package skills

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/jtsang4/lanesh/internal/protocol"
)

// Watch rescans sources whenever they change and calls onChange with the new
// index when its hash differs from the previous one. It also rescans every
// interval as a fallback for missed events. Watch blocks until ctx is done.
func Watch(ctx context.Context, sources []Source, current protocol.SkillIndex, interval time.Duration, onChange func(protocol.SkillIndex)) {
	w, err := fsnotify.NewWatcher()
	if err == nil {
		defer w.Close()
	}
	addWatches := func(idx protocol.SkillIndex) {
		if w == nil {
			return
		}
		for _, s := range sources {
			_ = w.Add(s.Dir)
			_ = w.Add(filepath.Dir(s.Dir)) // notice the skills directory being created
		}
		for _, s := range idx.Skills {
			if resolved, err := filepath.EvalSymlinks(s.Path); err == nil {
				_ = w.Add(resolved)
			}
		}
	}
	addWatches(current)

	var events <-chan fsnotify.Event
	var errs <-chan error
	if w != nil {
		events, errs = w.Events, w.Errors
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var debounce <-chan time.Time

	rescan := func() {
		idx := Scan(sources)
		if idx.Hash != current.Hash {
			current = idx
			addWatches(idx)
			onChange(idx)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-events:
			debounce = time.After(500 * time.Millisecond)
		case err := <-errs:
			slog.Debug("skill watcher error", "err", err)
		case <-debounce:
			debounce = nil
			rescan()
		case <-ticker.C:
			rescan()
		}
	}
}

// Exists reports whether any source directory exists.
func Exists(sources []Source) bool {
	for _, s := range sources {
		if _, err := os.Stat(s.Dir); err == nil {
			return true
		}
	}
	return false
}
