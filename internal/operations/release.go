package operations

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"netgo/internal/model"
	"netgo/internal/securefile"
	"netgo/internal/site"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var Products = []string{"netgo-server", "netgo-helper", "netgoctl", "netgo-client-service", "netgo-ddns"}

type ReleaseManifest struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	Schema   int    `json:"schema"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

func checksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func regular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("release entry must be a regular file")
	}
	return nil
}

// Verify checks the externally supplied manifest digest, then each exact
// product image. It rejects traversal, duplicates and ambiguous checksum lines.
func Verify(source, expected string) (ReleaseManifest, error) {
	var manifest ReleaseManifest
	if !regexp.MustCompile("^[a-fA-F0-9]{64}$").MatchString(expected) {
		return manifest, errors.New("--sha256 must be the trusted SHA256SUMS digest")
	}
	sums := filepath.Join(source, "SHA256SUMS")
	if err := regular(sums); err != nil {
		return manifest, err
	}
	digest, err := checksum(sums)
	if err != nil || !strings.EqualFold(digest, expected) {
		return manifest, errors.New("release manifest checksum mismatch")
	}
	f, err := os.Open(sums)
	if err != nil {
		return manifest, err
	}
	defer f.Close()
	allowed := map[string]bool{"release.json": true}
	for _, name := range Products {
		allowed[name] = true
	}
	found := map[string]bool{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		parts := strings.Split(line, "  ")
		if len(parts) != 2 || !allowed[parts[1]] || found[parts[1]] || !regexp.MustCompile("^[a-fA-F0-9]{64}$").MatchString(parts[0]) {
			return manifest, errors.New("invalid release checksum entry")
		}
		found[parts[1]] = true
		path := filepath.Join(source, parts[1])
		if err = regular(path); err != nil {
			return manifest, err
		}
		digest, err = checksum(path)
		if err != nil || !strings.EqualFold(digest, parts[0]) {
			return manifest, fmt.Errorf("release checksum failed: %s", parts[1])
		}
	}
	if err = scan.Err(); err != nil {
		return manifest, err
	}
	if len(found) != len(allowed) {
		return manifest, errors.New("incomplete release")
	}
	b, err := os.ReadFile(filepath.Join(source, "release.json"))
	if err != nil {
		return manifest, err
	}
	if err = site.Decode(strings.NewReader(string(b)), &manifest); err != nil {
		return manifest, err
	}
	if !regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$").MatchString(manifest.Version) || manifest.Schema != 1 || manifest.Protocol != model.Protocol || manifest.OS != "linux" || manifest.Arch != "amd64" {
		return manifest, errors.New("unsupported release compatibility")
	}
	return manifest, nil
}

type ReleaseTransaction struct {
	Root       string // defaults to /opt/netgo
	ConfigPath string
	Run        Command
	Health     func(context.Context, site.Config) error
	Snapshot   func(context.Context) (string, error) // called only after all writers stop
}
type releaseJournal struct {
	Previous string    `json:"previous"`
	Next     string    `json:"next"`
	Snapshot string    `json:"snapshot"`
	At       time.Time `json:"at"`
}

var releaseServices = []string{"netgo-server", "netgo-helper", "netgo-router", "netbird-server"}

func (t ReleaseTransaction) root() string {
	if t.Root != "" {
		return t.Root
	}
	return "/opt/netgo"
}
func (t ReleaseTransaction) current() (string, error) {
	root, err := filepath.Abs(t.root())
	if err != nil {
		return "", err
	}
	current := filepath.Join(root, "current")
	info, err := os.Lstat(current)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", errors.New("current release must be a managed symlink")
	}
	target, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(filepath.Join(root, "releases"), target)
	if err != nil || rel == "." || strings.ContainsAny(rel, "/\\") || strings.HasPrefix(rel, "..") {
		return "", errors.New("current release is outside managed releases")
	}
	return target, nil
}
func (t ReleaseTransaction) Upgrade(ctx context.Context, source, expected string) error {
	manifest, err := Verify(source, expected)
	if err != nil {
		return err
	}
	root := t.root()
	target := filepath.Join(root, "releases", manifest.Version)
	if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return errors.New("release already exists")
	}
	if _, err = t.current(); err != nil {
		return err
	}
	if err = os.Mkdir(target, 0755); err != nil {
		return err
	}
	staged := false
	defer func() {
		if !staged {
			for _, name := range append(append([]string{}, Products...), "release.json", "SHA256SUMS") {
				_ = os.Remove(filepath.Join(target, name))
			}
			_ = os.Remove(target)
		}
	}()
	for _, name := range append(append([]string{}, Products...), "release.json", "SHA256SUMS") {
		b, readErr := os.ReadFile(filepath.Join(source, name))
		if readErr != nil {
			return readErr
		}
		mode := os.FileMode(0755)
		if name == "release.json" || name == "SHA256SUMS" {
			mode = 0644
		}
		if err = securefile.Create(filepath.Join(target, name), b, mode); err != nil {
			return err
		}
	}
	// Re-check the staged bytes to close the gap between source verification/copy.
	if _, err = Verify(target, expected); err != nil {
		return err
	}
	// Stage privately under an inherited umask, then allow the unprivileged
	// service account to traverse this verified executable directory.
	if err = os.Chmod(target, 0755); err != nil {
		return err
	}
	staged = true
	return t.switchTo(ctx, target, true)
}
func (t ReleaseTransaction) Rollback(ctx context.Context) error {
	b, err := os.ReadFile(filepath.Join(t.root(), "previous-release.json"))
	if err != nil {
		return err
	}
	var record releaseJournal
	if err = site.Decode(strings.NewReader(string(b)), &record); err != nil {
		return err
	}
	target, err := filepath.EvalSymlinks(record.Previous)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(filepath.Join(t.root(), "releases"), target)
	if err != nil || rel == "." || strings.ContainsAny(rel, "/\\") || strings.HasPrefix(rel, "..") {
		return errors.New("rollback target outside managed releases")
	}
	digest, err := checksum(filepath.Join(target, "SHA256SUMS"))
	if err != nil {
		return err
	}
	if _, err = Verify(target, digest); err != nil {
		return err
	}
	return t.switchTo(ctx, target, false)
}
func replaceLink(root, target string) error {
	temporary := filepath.Join(root, ".next")
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		return errors.New("interrupted release link exists")
	}
	if err := os.Symlink(target, temporary); err != nil {
		return err
	}
	defer os.Remove(temporary)
	return os.Rename(temporary, filepath.Join(root, "current"))
}
func (t ReleaseTransaction) switchTo(ctx context.Context, target string, snapshot bool) error {
	unlock, err := lockOperation(t.ConfigPath)
	if err != nil {
		return err
	}
	defer unlock()
	old, err := t.current()
	if err != nil {
		return err
	}
	if target == old {
		return errors.New("target is already active")
	}
	c, err := site.Load(t.ConfigPath)
	if err != nil {
		return err
	}
	run := t.Run
	if run == nil {
		run = Exec
	}
	health := t.Health
	if health == nil {
		health = Health
	}
	lock := filepath.Join(t.root(), ".release-transaction")
	if err = os.Mkdir(lock, 0700); err != nil {
		return errors.New("release transaction exists; inspect recovery journal")
	}
	completed := false
	defer func() {
		if completed {
			_ = os.Remove(filepath.Join(lock, "journal.json"))
			_ = os.Remove(lock)
		}
	}()
	record := releaseJournal{Previous: old, Next: target, At: time.Now().UTC()}
	save := func() error {
		b, _ := json.MarshalIndent(record, "", "  ")
		return securefile.Write(filepath.Join(lock, "journal.json"), b, 0600)
	}
	if err = save(); err != nil {
		return err
	}
	restart := func(ctx context.Context) error {
		return run(ctx, "systemctl", "start", "netbird-server", "netgo-router", "netgo-helper", "netgo-server")
	}
	recoverOld := func(cause error) error {
		recovery, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		stopErr := run(recovery, "systemctl", append([]string{"stop"}, releaseServices...)...)
		linkErr := replaceLink(t.root(), old)
		startErr := restart(recovery)
		healthErr := waitHealth(recovery, health, c)
		if stopErr != nil || linkErr != nil || startErr != nil || healthErr != nil {
			return errors.Join(cause, errors.New("release recovery incomplete; retain .release-transaction"), stopErr, linkErr, startErr, healthErr)
		}
		completed = true
		return fmt.Errorf("release change rolled back: %w", cause)
	}
	if err = run(ctx, "systemctl", append([]string{"stop"}, releaseServices...)...); err != nil {
		return recoverOld(err)
	}
	if snapshot {
		if t.Snapshot == nil {
			return recoverOld(errors.New("consistent encrypted snapshot required"))
		}
		record.Snapshot, err = t.Snapshot(ctx)
		if err != nil {
			return recoverOld(err)
		}
		if err = save(); err != nil {
			return recoverOld(err)
		}
	}
	if err = run(ctx, filepath.Join(target, "netgoctl"), "config", "validate", "--config", t.ConfigPath); err != nil {
		return recoverOld(err)
	}
	if err = replaceLink(t.root(), target); err != nil {
		return recoverOld(err)
	}
	if err = restart(ctx); err != nil {
		return recoverOld(err)
	}
	if err = waitHealth(ctx, health, c); err != nil {
		return recoverOld(err)
	}
	b, _ := json.MarshalIndent(record, "", "  ")
	if err = securefile.Write(filepath.Join(t.root(), "previous-release.json"), b, 0600); err != nil {
		return recoverOld(err)
	}
	completed = true
	return nil
}
