package admin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func testBaseline() map[Category][]string {
	return map[Category][]string{
		CategoryBlock:  {"ads.example.com", "tracker.example.com"},
		CategoryDirect: {"internal.example.com"},
		CategoryProxy:  {"**.google.com"},
	}
}

func stateFilePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "admin-state.json")
}

func readStateFile(t *testing.T, path string) State {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}
	return s
}

func TestStateStoreRuleAddAndRemoveAlgebra(t *testing.T) {
	t.Parallel()
	path := stateFilePath(t)
	st := LoadStateStore(path)
	st.SetBaseline(testBaseline())

	// New rule: goes to Add and must enter the runtime set.
	add, err := st.RuleAdd(CategoryProxy, "**.example.com")
	if err != nil || !slices.Equal(add, []string{"**.example.com"}) {
		t.Fatalf("add new rule: runtimeAdd=%v err=%v", add, err)
	}

	// Duplicate add is a no-op.
	add, err = st.RuleAdd(CategoryProxy, "**.example.com")
	if err != nil || add != nil {
		t.Fatalf("duplicate add: runtimeAdd=%v err=%v", add, err)
	}

	// Baseline rule without tombstone is a no-op.
	add, err = st.RuleAdd(CategoryBlock, "ads.example.com")
	if err != nil || add != nil {
		t.Fatalf("add existing baseline rule: runtimeAdd=%v err=%v", add, err)
	}

	// Tombstone a baseline rule.
	found, err := st.RuleRemove(CategoryBlock, "ads.example.com")
	if err != nil || !found {
		t.Fatalf("tombstone baseline rule: found=%v err=%v", found, err)
	}

	// Re-adding the tombstoned baseline rule clears the tombstone and
	// returns it for runtime re-add.
	add, err = st.RuleAdd(CategoryBlock, "ads.example.com")
	if err != nil || !slices.Equal(add, []string{"ads.example.com"}) {
		t.Fatalf("restore tombstoned rule: runtimeAdd=%v err=%v", add, err)
	}

	// Removing an admin-added rule drops it from Add.
	found, err = st.RuleRemove(CategoryProxy, "**.example.com")
	if err != nil || !found {
		t.Fatalf("remove admin-added rule: found=%v err=%v", found, err)
	}

	// Unknown rule and double tombstone are not found.
	found, err = st.RuleRemove(CategoryProxy, "**.nonexistent.com")
	if err != nil || found {
		t.Fatalf("remove unknown rule: found=%v err=%v", found, err)
	}

	changes := st.Changes()
	if !changes.Persistent {
		t.Fatal("expected persistent store")
	}
	if d := changes.Rules[CategoryBlock]; len(d.Add) != 0 || len(d.Remove) != 0 {
		t.Fatalf("unexpected block delta after restore: %+v", d)
	}
	if d := changes.Rules[CategoryProxy]; len(d.Add) != 0 || len(d.Remove) != 0 {
		t.Fatalf("unexpected proxy delta after remove: %+v", d)
	}

	// The file on disk reflects the final state.
	onDisk := readStateFile(t, path)
	if len(onDisk.Rules) != 0 {
		t.Fatalf("expected empty deltas on disk, got %+v", onDisk.Rules)
	}
	if onDisk.Revision == 0 {
		t.Fatal("expected revision to bump on mutations")
	}
}

func TestStateStoreRestartRestoresDeltas(t *testing.T) {
	t.Parallel()
	path := stateFilePath(t)

	st := LoadStateStore(path)
	st.SetBaseline(testBaseline())
	if _, err := st.RuleAdd(CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RuleRemove(CategoryBlock, "ads.example.com"); err != nil {
		t.Fatal(err)
	}

	// Simulate a restart: reload from disk, same baseline.
	st2 := LoadStateStore(path)
	st2.SetBaseline(testBaseline())
	if d := st2.Delta(CategoryProxy); !slices.Equal(d.Add, []string{"**.example.com"}) {
		t.Fatalf("restored proxy add: %+v", d)
	}
	if d := st2.Delta(CategoryBlock); !slices.Equal(d.Remove, []string{"ads.example.com"}) {
		t.Fatalf("restored block remove: %+v", d)
	}
}

func TestStateStoreGCCollectsStaleDeltas(t *testing.T) {
	t.Parallel()
	path := stateFilePath(t)

	st := LoadStateStore(path)
	st.SetBaseline(testBaseline())
	if _, err := st.RuleAdd(CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RuleRemove(CategoryBlock, "ads.example.com"); err != nil {
		t.Fatal(err)
	}

	// New baseline: the admin-added rule entered the file, the tombstoned
	// rule left it. Both deltas are now stale no-ops and must be dropped.
	st2 := LoadStateStore(path)
	st2.SetBaseline(map[Category][]string{
		CategoryBlock:  {"tracker.example.com"},
		CategoryDirect: {"internal.example.com"},
		CategoryProxy:  {"**.google.com", "**.example.com"},
	})
	changes := st2.Changes()
	if d := changes.Rules[CategoryProxy]; len(d.Add) != 0 {
		t.Fatalf("stale add not collected: %+v", d)
	}
	if d := changes.Rules[CategoryBlock]; len(d.Remove) != 0 {
		t.Fatalf("stale tombstone not collected: %+v", d)
	}
	onDisk := readStateFile(t, path)
	if len(onDisk.Rules) != 0 {
		t.Fatalf("GC result not persisted: %+v", onDisk.Rules)
	}
}

func TestStateStorePersistFailureLeavesStateUnchanged(t *testing.T) {
	t.Parallel()
	// A state path under a regular file cannot be created, so every persist
	// fails deterministically (even as root).
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := LoadStateStore(filepath.Join(blocker, "admin-state.json"))
	st.SetBaseline(testBaseline())

	add, err := st.RuleAdd(CategoryProxy, "**.example.com")
	if err == nil {
		t.Fatal("expected persist error")
	}
	if add != nil {
		t.Fatalf("runtime add must be nil on persist failure, got %v", add)
	}
	if d := st.Delta(CategoryProxy); len(d.Add) != 0 {
		t.Fatalf("state mutated despite persist failure: %+v", d)
	}

	if err := st.RuleReset(CategoryProxy); err != nil {
		t.Fatalf("reset with no delta must be a no-op: %v", err)
	}
}

func TestStateStoreCorruptFileIsQuarantined(t *testing.T) {
	t.Parallel()
	path := stateFilePath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := LoadStateStore(path)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt file should be moved aside, stat err=%v", err)
	}
	matches, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one quarantine file, got %v err=%v", matches, err)
	}
	// The store works from empty state.
	if _, err := st.RuleAdd(CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	if d := st.Delta(CategoryProxy); !slices.Equal(d.Add, []string{"**.example.com"}) {
		t.Fatalf("unexpected delta after quarantine: %+v", d)
	}
}

func TestStateStoreApplyConfigRevision(t *testing.T) {
	t.Parallel()
	path := stateFilePath(t)
	st := LoadStateStore(path)
	strPtr := func(s string) *string { return &s }

	if _, err := st.ApplyConfig(ConfigOverrides{LogLevel: strPtr("debug")}, 1); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale revision must fail: %v", err)
	}
	rev, err := st.ApplyConfig(ConfigOverrides{LogLevel: strPtr("debug")}, 0)
	if err != nil || rev != 1 {
		t.Fatalf("apply config: rev=%d err=%v", rev, err)
	}
	if got := st.ConfigOverrides(); got.LogLevel == nil || *got.LogLevel != "debug" {
		t.Fatalf("unexpected overrides: %+v", got)
	}

	// Overrides survive a reload.
	st2 := LoadStateStore(path)
	if got := st2.ConfigOverrides(); got.LogLevel == nil || *got.LogLevel != "debug" {
		t.Fatalf("overrides not persisted: %+v", got)
	}
	if st2.Revision() != 1 {
		t.Fatalf("revision not persisted: %d", st2.Revision())
	}
}

func TestStateStoreConcurrentMutations(t *testing.T) {
	t.Parallel()
	path := stateFilePath(t)
	st := LoadStateStore(path)
	st.SetBaseline(testBaseline())

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			rule := "**.rule" + string(rune('a'+i%26)) + ".com"
			if _, err := st.RuleAdd(CategoryProxy, rule); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_ = st.Changes()
			_ = st.Revision()
		}()
	}
	wg.Wait()

	onDisk := readStateFile(t, path)
	if onDisk.Revision != st.Revision() {
		t.Fatalf("disk revision %d != memory revision %d", onDisk.Revision, st.Revision())
	}
}

func TestStateStoreInMemoryWhenPathEmpty(t *testing.T) {
	t.Parallel()
	st := LoadStateStore("")
	if st.Persistent() {
		t.Fatal("empty path must not be persistent")
	}
	if _, err := st.RuleAdd(CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	changes := st.Changes()
	if changes.Persistent {
		t.Fatal("in-memory store must report persistent=false")
	}
	if d := changes.Rules[CategoryProxy]; !slices.Equal(d.Add, []string{"**.example.com"}) {
		t.Fatalf("unexpected delta: %+v", d)
	}
}

func TestStateStoreRuleRemoveBatchIsAtomic(t *testing.T) {
	t.Parallel()
	st := LoadStateStore(stateFilePath(t))
	st.SetBaseline(testBaseline())
	if _, err := st.RuleAdd(CategoryProxy, "**.custom.example"); err != nil {
		t.Fatal(err)
	}
	before := st.Revision()

	removed, err := st.RuleRemoveBatch(CategoryProxy,
		"**.google.com", "**.custom.example", "**.google.com", "**.unknown.example")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removed, []string{"**.google.com", "**.custom.example"}) {
		t.Fatalf("unexpected runtime removals: %v", removed)
	}
	if st.Revision() != before+1 {
		t.Fatalf("batch must bump revision once: before=%d after=%d", before, st.Revision())
	}
	changes := st.Changes().Rules[CategoryProxy]
	if len(changes.Add) != 0 || !slices.Equal(changes.Remove, []string{"**.google.com"}) {
		t.Fatalf("unexpected persisted batch delta: %+v", changes)
	}

	// Duplicates and unknown rules are a no-op and do not write again.
	before = st.Revision()
	removed, err = st.RuleRemoveBatch(CategoryProxy, "**.google.com", "**.unknown.example")
	if err != nil || removed != nil {
		t.Fatalf("expected no-op batch, removed=%v err=%v", removed, err)
	}
	if st.Revision() != before {
		t.Fatalf("no-op batch changed revision: before=%d after=%d", before, st.Revision())
	}
}
