package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const stateVersion = 1

// RuleDelta records admin-side rule changes relative to the boot baseline:
// Add holds rules absent from the baseline, Remove holds tombstoned baseline
// rules. Both lists are deduplicated and a rule never appears in both.
type RuleDelta struct {
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

// ConfigOverrides holds the whitelisted config fields editable through the
// admin console. Pointer fields distinguish "not submitted" (nil, keep the
// previous override) from an explicit change: a non-empty value sets the
// override, and a non-nil empty value/empty list clears it so the
// file/flag configuration takes over again. Secrets must never be added
// here. List fields (rule sources) hold the full inline lists.
type ConfigOverrides struct {
	LogLevel    *string `json:"log_level,omitempty"`
	DNSUpstream *string `json:"dns_upstream,omitempty"`
	DNSFallback *string `json:"dns_fallback,omitempty"`
	DNSReverse  *string `json:"dns_reverse,omitempty"`

	RemoteType                  *string `json:"remote_type,omitempty"`
	RemoteAddr                  *string `json:"remote_addr,omitempty"`
	RemoteTLSServerName         *string `json:"remote_tls_server_name,omitempty"`
	RemoteTLSClientHello        *string `json:"remote_tls_client_hello,omitempty"`
	RemoteTLSInsecureSkipVerify *string `json:"remote_tls_insecure_skip_verify,omitempty"`

	DNSServe  *string `json:"dns_serve,omitempty"`
	DNSServe6 *string `json:"dns_serve6,omitempty"`

	Socks5Addr *string `json:"socks5_addr,omitempty"`

	AdminSessionFile               *string `json:"admin_session_file,omitempty"`
	AdminDisableSessionPersistence *string `json:"admin_disable_session_persistence,omitempty"`
	AdminCookieSecure              *string `json:"admin_cookie_secure,omitempty"`
	AdminStateFile                 *string `json:"admin_state_file,omitempty"`

	RouterBlockFile           *string   `json:"router_block_file,omitempty"`
	RouterBlockFilePrefix     *string   `json:"router_block_file_prefix,omitempty"`
	RouterBlockFileSkipRules  *[]string `json:"router_block_file_skip_rules,omitempty"`
	RouterBlockRules          *[]string `json:"router_block_rules,omitempty"`
	RouterDirectFile          *string   `json:"router_direct_file,omitempty"`
	RouterDirectFilePrefix    *string   `json:"router_direct_file_prefix,omitempty"`
	RouterDirectFileSkipRules *[]string `json:"router_direct_file_skip_rules,omitempty"`
	RouterDirectRules         *[]string `json:"router_direct_rules,omitempty"`
	RouterProxyFile           *string   `json:"router_proxy_file,omitempty"`
	RouterProxyFilePrefix     *string   `json:"router_proxy_file_prefix,omitempty"`
	RouterProxyFileSkipRules  *[]string `json:"router_proxy_file_skip_rules,omitempty"`
	RouterProxyRules          *[]string `json:"router_proxy_rules,omitempty"`
	RouterCountryMMDB         *string   `json:"router_country_mmdb,omitempty"`
	RouterCountryFile         *string   `json:"router_country_file,omitempty"`
	RouterCountryRules        *[]string `json:"router_country_rules,omitempty"`
}

// State is the on-disk admin state document. Revision bumps on every
// mutation and drives optimistic concurrency for config PATCHes.
type State struct {
	Version   int                     `json:"version"`
	Revision  uint64                  `json:"revision"`
	UpdatedAt time.Time               `json:"updatedAt"`
	Rules     map[Category]*RuleDelta `json:"rules"`
	Config    ConfigOverrides         `json:"config"`
}

// RuleChangeSet is the API view of the current rule deltas.
type RuleChangeSet struct {
	Persistent bool                   `json:"persistent"`
	Revision   uint64                 `json:"revision"`
	Rules      map[Category]RuleDelta `json:"rules"`
}

// ErrRevisionMismatch is returned when a config PATCH carries a stale
// revision, i.e. another change landed after the caller loaded the view.
var ErrRevisionMismatch = errors.New("state revision mismatch")

// StateStore serializes all admin state mutations behind one mutex and
// persists them atomically (tmp file + fsync + rename, 0600). Mutations are
// applied to a cloned candidate first and only committed after a successful
// write, so a persist failure leaves the previous state intact and callers
// must not apply the corresponding runtime change.
type StateStore struct {
	mu       sync.Mutex
	path     string
	state    State
	baseline map[Category]map[string]struct{}
}

// LoadStateStore opens the state file at path. An empty path yields an
// in-memory-only store (mutations work, nothing is written). A missing file
// starts empty; a corrupt or unsupported file is quarantined aside and
// starts empty as well, so a bad state file never blocks startup.
func LoadStateStore(path string) *StateStore {
	st := &StateStore{path: path, state: newState()}
	if path == "" {
		return st
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("read admin state", "file", path, "error", err)
		}
		return st
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil || s.Version != stateVersion || !validStateCategories(s.Rules) {
		st.quarantine()
		return st
	}
	st.state = normalizeState(s)
	return st
}

func newState() State {
	return State{Version: stateVersion, Rules: make(map[Category]*RuleDelta)}
}

// normalizeState repairs load-time inconsistencies: nil maps, duplicate
// delta entries, and rules present in both Add and Remove (Add wins: the
// most recent mutation appended it there).
func normalizeState(s State) State {
	if s.Rules == nil {
		s.Rules = make(map[Category]*RuleDelta)
	}
	for cat, d := range s.Rules {
		if d == nil {
			delete(s.Rules, cat)
			continue
		}
		d.Add = dedupeStrings(d.Add)
		d.Remove = dedupeStrings(d.Remove)
		keep := make(map[string]struct{}, len(d.Add))
		for _, r := range d.Add {
			keep[r] = struct{}{}
		}
		d.Remove = slices.DeleteFunc(d.Remove, func(r string) bool {
			_, ok := keep[r]
			return ok
		})
		if len(d.Add) == 0 && len(d.Remove) == 0 {
			delete(s.Rules, cat)
		}
	}
	return s
}

func validStateCategories(rules map[Category]*RuleDelta) bool {
	for cat := range rules {
		if !cat.valid() {
			return false
		}
	}
	return true
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// Persistent reports whether mutations are written to disk.
func (st *StateStore) Persistent() bool { return st.path != "" }

// quarantine renames a corrupt state file aside so the next start does not
// trip over it again.
func (st *StateStore) quarantine() {
	dst := fmt.Sprintf("%s.corrupt-%d", st.path, time.Now().Unix())
	if err := os.Rename(st.path, dst); err != nil {
		slog.Warn("quarantine corrupt admin state", "file", st.path, "error", err)
		return
	}
	slog.Warn("quarantined corrupt admin state, starting empty", "file", st.path, "moved_to", dst)
}

// SetBaseline records the boot rule lists (file + inline config) per
// category and garbage-collects stale delta entries: tombstones whose rule
// left the baseline and additions that entered it. GC does not bump the
// revision; a persist failure keeps the cleaned in-memory state anyway
// because the dropped entries were no-ops.
func (st *StateStore) SetBaseline(baseline map[Category][]string) {
	sets := make(map[Category]map[string]struct{}, len(baseline))
	for cat, rules := range baseline {
		m := make(map[string]struct{}, len(rules))
		for _, r := range rules {
			m[r] = struct{}{}
		}
		sets[cat] = m
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	st.baseline = sets

	cand := st.cloneLocked()
	changed := false
	for cat, d := range cand.Rules {
		base := sets[cat]
		adds := slices.DeleteFunc(slices.Clone(d.Add), func(r string) bool {
			_, ok := base[r]
			return ok
		})
		rems := slices.DeleteFunc(slices.Clone(d.Remove), func(r string) bool {
			_, ok := base[r]
			return !ok
		})
		if !slices.Equal(adds, d.Add) || !slices.Equal(rems, d.Remove) {
			changed = true
		}
		d.Add, d.Remove = adds, rems
		if len(d.Add) == 0 && len(d.Remove) == 0 {
			delete(cand.Rules, cat)
		}
	}
	if !changed {
		return
	}
	cand.UpdatedAt = time.Now()
	if err := st.persistLocked(cand); err != nil {
		slog.Warn("persist admin state GC", "error", err)
	}
	st.state = cand
}

// ConfigOverrides returns the current whitelisted config overrides.
func (st *StateStore) ConfigOverrides() ConfigOverrides {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.state.Config
}

// ApplyConfig replaces the config overrides and persists, guarded by the
// caller's revision. It returns the new revision.
func (st *StateStore) ApplyConfig(o ConfigOverrides, revision uint64) (uint64, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if revision != st.state.Revision {
		return 0, ErrRevisionMismatch
	}
	cand := st.cloneLocked()
	cand.Config = o
	cand.bump()
	if err := st.persistLocked(cand); err != nil {
		return 0, err
	}
	st.state = cand
	return cand.Revision, nil
}

// Revision returns the current state revision.
func (st *StateStore) Revision() uint64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.state.Revision
}

// RuleAdd records rules as additions (or restores tombstoned baseline rules)
// and persists. It returns the rules that must enter the runtime rule set;
// rules already effective are dropped from the result. A nil result with nil
// error means the call was a no-op.
func (st *StateStore) RuleAdd(category Category, rules ...string) ([]string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	cand := st.cloneLocked()
	d := cand.delta(category)
	var runtimeAdd []string
	for _, rule := range rules {
		_, inBase := st.baseline[category][rule]
		switch {
		case inBase && slices.Contains(d.Remove, rule):
			d.Remove = removeString(d.Remove, rule)
			runtimeAdd = append(runtimeAdd, rule)
		case inBase, slices.Contains(d.Add, rule):
			// already effective
		default:
			d.Add = append(d.Add, rule)
			runtimeAdd = append(runtimeAdd, rule)
		}
	}
	if len(runtimeAdd) == 0 {
		return nil, nil
	}
	if len(d.Add) == 0 && len(d.Remove) == 0 {
		delete(cand.Rules, category)
	}
	cand.bump()
	if err := st.persistLocked(cand); err != nil {
		return nil, err
	}
	st.state = cand
	return runtimeAdd, nil
}

// RuleRemove records one rule removal and persists it as one candidate
// mutation. It is the single-rule convenience wrapper for RuleRemoveBatch.
func (st *StateStore) RuleRemove(category Category, rule string) (bool, error) {
	removed, err := st.RuleRemoveBatch(category, rule)
	return len(removed) > 0, err
}

// RuleRemoveBatch applies all effective removals to one candidate state and
// persists once. Unknown rules and duplicates are ignored. The returned list
// is the exact runtime work that may be applied after persistence succeeds.
func (st *StateStore) RuleRemoveBatch(category Category, rules ...string) ([]string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	cand := st.cloneLocked()
	d := cand.delta(category)
	removed := make([]string, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if _, ok := seen[rule]; ok {
			continue
		}
		seen[rule] = struct{}{}
		_, inBase := st.baseline[category][rule]
		switch {
		case slices.Contains(d.Add, rule):
			d.Add = removeString(d.Add, rule)
			removed = append(removed, rule)
		case inBase && !slices.Contains(d.Remove, rule):
			d.Remove = append(d.Remove, rule)
			removed = append(removed, rule)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if len(d.Add) == 0 && len(d.Remove) == 0 {
		delete(cand.Rules, category)
	}
	cand.bump()
	if err := st.persistLocked(cand); err != nil {
		return nil, err
	}
	st.state = cand
	return removed, nil
}

// RuleReset clears the deltas of the given categories and persists. Clearing
// a category that has no delta is a no-op.
func (st *StateStore) RuleReset(categories ...Category) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	cand := st.cloneLocked()
	changed := false
	for _, cat := range categories {
		if _, ok := cand.Rules[cat]; ok {
			delete(cand.Rules, cat)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	cand.bump()
	if err := st.persistLocked(cand); err != nil {
		return err
	}
	st.state = cand
	return nil
}

// Changes returns a snapshot of the current rule deltas. All three
// categories are always present so clients can render unconditionally.
func (st *StateStore) Changes() RuleChangeSet {
	st.mu.Lock()
	defer st.mu.Unlock()

	out := RuleChangeSet{
		Persistent: st.path != "",
		Revision:   st.state.Revision,
		Rules:      make(map[Category]RuleDelta, 3),
	}
	for _, cat := range []Category{CategoryBlock, CategoryDirect, CategoryProxy} {
		if d, ok := st.state.Rules[cat]; ok {
			out.Rules[cat] = RuleDelta{
				Add:    nonNilStrings(d.Add),
				Remove: nonNilStrings(d.Remove),
			}
		} else {
			out.Rules[cat] = RuleDelta{Add: []string{}, Remove: []string{}}
		}
	}
	return out
}

// nonNilStrings clones a slice, mapping nil to an empty slice so API
// responses never carry JSON null where an array is expected.
func nonNilStrings(in []string) []string {
	if out := slices.Clone(in); out != nil {
		return out
	}
	return []string{}
}

// Delta returns a copy of one category's delta for runtime reconstruction.
func (st *StateStore) Delta(category Category) RuleDelta {
	st.mu.Lock()
	defer st.mu.Unlock()
	if d, ok := st.state.Rules[category]; ok {
		return RuleDelta{Add: nonNilStrings(d.Add), Remove: nonNilStrings(d.Remove)}
	}
	return RuleDelta{}
}

func (st *StateStore) cloneLocked() State {
	cand := st.state
	cand.Rules = make(map[Category]*RuleDelta, len(st.state.Rules))
	for cat, d := range st.state.Rules {
		cand.Rules[cat] = &RuleDelta{
			Add:    slices.Clone(d.Add),
			Remove: slices.Clone(d.Remove),
		}
	}
	return cand
}

func (s *State) delta(category Category) *RuleDelta {
	d, ok := s.Rules[category]
	if !ok {
		d = &RuleDelta{}
		s.Rules[category] = d
	}
	return d
}

func (s *State) bump() {
	s.Revision++
	s.UpdatedAt = time.Now()
}

func removeString(list []string, s string) []string {
	if i := slices.Index(list, s); i >= 0 {
		return slices.Delete(list, i, i+1)
	}
	return list
}

// persistLocked writes the state atomically: a temp file in the same
// directory, fsync, rename, then a directory fsync so the rename itself is
// durable. It is a no-op for in-memory stores.
func (st *StateStore) persistLocked(s State) error {
	if st.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal admin state: %w", err)
	}

	dir := filepath.Dir(st.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create admin state dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".admin-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create admin state temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write admin state temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync admin state temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close admin state temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod admin state temp file: %w", err)
	}
	if err := os.Rename(tmpName, st.path); err != nil {
		return fmt.Errorf("rename admin state file: %w", err)
	}

	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
