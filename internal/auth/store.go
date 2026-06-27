package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Credentials is the persisted session state captured at chromedp login.
type Credentials struct {
	Cookies    map[string]string `json:"cookies"`
	Headers    map[string]string `json:"headers"`
	CapturedAt time.Time         `json:"captured_at"`
	// LiAtExpiry is the nominal expiry of the li_at cookie as reported by the
	// browser at login time (epoch-seconds from CDP). Zero means unknown.
	// NOTE: the staleness policy is age-based (14 days from CapturedAt), not
	// expiry-based -- LiAtExpiry is informational only.
	LiAtExpiry time.Time `json:"li_at_expiry,omitempty"`
}

// DefaultPath returns the canonical credentials.json path.
// If the home directory cannot be resolved (e.g. HOME unset), the path is
// rooted at the filesystem root and Save/Load will surface the failure.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "li-assist", "credentials.json")
}

// Save writes credentials atomically with file mode 0600. The file holds a
// live LinkedIn session, so the 0600 mode is enforced with an explicit Chmod
// to defeat a restrictive process umask.
func Save(c Credentials, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the primary error is returned
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup; the primary error is returned
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}

// Load reads credentials from path.
func Load(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("read: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, fmt.Errorf("unmarshal: %w", err)
	}
	return c, nil
}
