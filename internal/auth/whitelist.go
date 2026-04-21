package auth

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Whitelist restricts which Feishu users may sign in.
//
// JSON file shape (flexible, match any one of the fields):
//   {
//     "emails":    ["alice@example.com", "bob@example.com"],
//     "open_ids":  ["ou_xxx"],
//     "union_ids": ["on_xxx"],
//     "mobiles":   ["+8613800138000"]
//   }
//
// If the file is missing or empty, login is rejected with a clear message
// so the admin must intentionally populate it before anyone can log in.
type Whitelist struct {
	path string

	mu        sync.RWMutex
	loadedAt  time.Time
	emails    map[string]struct{}
	openIDs   map[string]struct{}
	unionIDs  map[string]struct{}
	mobiles   map[string]struct{}
	fileExist bool
	empty     bool
}

type whitelistFile struct {
	Emails   []string `json:"emails"`
	OpenIDs  []string `json:"open_ids"`
	UnionIDs []string `json:"union_ids"`
	Mobiles  []string `json:"mobiles"`
}

// NewWhitelist creates a whitelist bound to a JSON file. The file is lazily
// reloaded on every Allow() call if its mtime has changed, so admins can edit
// the file without restarting the server.
func NewWhitelist(path string) *Whitelist {
	wl := &Whitelist{path: path}
	wl.reload()
	return wl
}

func (w *Whitelist) reload() {
	data, err := os.ReadFile(w.path)
	w.mu.Lock()
	defer w.mu.Unlock()

	w.loadedAt = time.Now()
	w.emails = map[string]struct{}{}
	w.openIDs = map[string]struct{}{}
	w.unionIDs = map[string]struct{}{}
	w.mobiles = map[string]struct{}{}

	if err != nil {
		if os.IsNotExist(err) {
			w.fileExist = false
			w.empty = true
			return
		}
		log.Printf("WARNING: whitelist read failed: %v", err)
		w.fileExist = true
		w.empty = true
		return
	}

	w.fileExist = true
	var f whitelistFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Printf("WARNING: whitelist parse failed: %v", err)
		w.empty = true
		return
	}

	for _, e := range f.Emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			w.emails[e] = struct{}{}
		}
	}
	for _, o := range f.OpenIDs {
		if o = strings.TrimSpace(o); o != "" {
			w.openIDs[o] = struct{}{}
		}
	}
	for _, u := range f.UnionIDs {
		if u = strings.TrimSpace(u); u != "" {
			w.unionIDs[u] = struct{}{}
		}
	}
	for _, m := range f.Mobiles {
		if m = strings.TrimSpace(m); m != "" {
			w.mobiles[m] = struct{}{}
		}
	}

	w.empty = len(w.emails)+len(w.openIDs)+len(w.unionIDs)+len(w.mobiles) == 0
	if w.empty {
		log.Printf("WARNING: whitelist %s is empty — no users can log in", w.path)
	} else {
		log.Printf("INFO: whitelist loaded: %d emails, %d open_ids, %d union_ids, %d mobiles",
			len(w.emails), len(w.openIDs), len(w.unionIDs), len(w.mobiles))
	}
}

// maybeReload refreshes the whitelist if the file mtime is newer than
// the last load. Cheap enough to run on every check.
func (w *Whitelist) maybeReload() {
	info, err := os.Stat(w.path)
	if err != nil {
		return
	}
	w.mu.RLock()
	stale := info.ModTime().After(w.loadedAt)
	w.mu.RUnlock()
	if stale {
		w.reload()
	}
}

// Allow reports whether a user matching any of the provided identifiers may
// sign in. Returns the matched identifier type for logging, or "" when denied.
func (w *Whitelist) Allow(email, openID, unionID, mobile string) (bool, string) {
	w.maybeReload()
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.empty {
		return false, ""
	}

	email = strings.ToLower(strings.TrimSpace(email))
	openID = strings.TrimSpace(openID)
	unionID = strings.TrimSpace(unionID)
	mobile = strings.TrimSpace(mobile)

	if email != "" {
		if _, ok := w.emails[email]; ok {
			return true, "email"
		}
	}
	if openID != "" {
		if _, ok := w.openIDs[openID]; ok {
			return true, "open_id"
		}
	}
	if unionID != "" {
		if _, ok := w.unionIDs[unionID]; ok {
			return true, "union_id"
		}
	}
	if mobile != "" {
		if _, ok := w.mobiles[mobile]; ok {
			return true, "mobile"
		}
	}
	return false, ""
}

// Status returns whether the whitelist file exists and is non-empty,
// plus a human-readable note for /health.
func (w *Whitelist) Status() (ok bool, note string) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.fileExist {
		return false, fmt.Sprintf("whitelist file missing: %s", w.path)
	}
	if w.empty {
		return false, fmt.Sprintf("whitelist file %s is empty", w.path)
	}
	return true, ""
}
