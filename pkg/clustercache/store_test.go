package clustercache

import (
	"fmt"
	"sync"
	"testing"

	cc "github.com/opencost/opencost/core/pkg/clustercache"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func makePodList(n int) []any {
	list := make([]any, 0, n)
	for i := 0; i < n; i++ {
		list = append(list, &v1.Pod{ObjectMeta: metav1.ObjectMeta{
			UID:       types.UID(fmt.Sprintf("uid-%d", i)),
			Name:      fmt.Sprintf("pod-%d", i),
			Namespace: "ns",
		}})
	}
	return list
}

// TestGenericStoreReplaceIsAtomic asserts that a reader calling GetAll while
// the reflector re-lists never observes a partially populated store. The
// reflector calls Replace on every re-list (watch expiry, 410 Gone,
// reconnect), which on a large cluster is a multi-thousand item rebuild;
// readers must see either the previous full set or the new full set.
func TestGenericStoreReplaceIsAtomic(t *testing.T) {
	const size = 5000

	store := NewGenericStore(cc.TransformPod)
	if err := store.Replace(makePodList(size), ""); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var partial int
	var mu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if n := len(store.GetAll()); n != size {
				mu.Lock()
				partial++
				mu.Unlock()
			}
		}
	}()

	for i := 0; i < 20; i++ {
		if err := store.Replace(makePodList(size), ""); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	if partial > 0 {
		t.Fatalf("GetAll observed a partially populated store %d times during Replace", partial)
	}
}

// TestGenericStoreReplaceDropsStaleItems asserts that Replace swaps the item
// set rather than merging into it. Replace is now a standalone implementation
// of the store contract (it no longer delegates to Add), so an item that is
// absent from the new list must be gone from the store; otherwise a re-list
// that observes a shrunken cluster leaves deleted pods billable forever.
func TestGenericStoreReplaceDropsStaleItems(t *testing.T) {
	store := NewGenericStore(cc.TransformPod)

	if err := store.Replace(makePodList(10), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(makePodList(3), ""); err != nil {
		t.Fatal(err)
	}

	all := store.GetAll()
	if len(all) != 3 {
		t.Fatalf("GetAll() = %d items after replacing 10 with 3, want 3", len(all))
	}

	present := make(map[types.UID]bool, len(all))
	for _, p := range all {
		present[p.UID] = true
	}
	for i := 0; i < 3; i++ {
		uid := types.UID(fmt.Sprintf("uid-%d", i))
		if !present[uid] {
			t.Errorf("%s missing from store after Replace, want it kept", uid)
		}
	}
	for i := 3; i < 10; i++ {
		uid := types.UID(fmt.Sprintf("uid-%d", i))
		if present[uid] {
			t.Errorf("stale %s still in store after Replace, want it dropped", uid)
		}
	}
}

// TestGenericStoreReplaceWithEmptyListClearsStore asserts the degenerate
// re-list case: a reflector that lists a resource with no remaining objects
// must empty the store, not leave the previous contents in place.
func TestGenericStoreReplaceWithEmptyListClearsStore(t *testing.T) {
	store := NewGenericStore(cc.TransformPod)

	if err := store.Replace(makePodList(10), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace([]any{}, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(store.GetAll()); n != 0 {
		t.Fatalf("GetAll() = %d items after Replace with an empty list, want 0", n)
	}

	if err := store.Replace(makePodList(10), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(nil, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(store.GetAll()); n != 0 {
		t.Fatalf("GetAll() = %d items after Replace with a nil list, want 0", n)
	}
}

// TestGenericStoreReplaceCallsOnInitOnceAfterSwap asserts the onInit hook
// fires exactly once, and only after the initial list has been swapped in, so
// that whatever the caller starts on init reads a fully populated store rather
// than an empty or half-built one.
func TestGenericStoreReplaceCallsOnInitOnceAfterSwap(t *testing.T) {
	const size = 10

	store := NewGenericStore(cc.TransformPod)

	var calls int
	var observed int
	store.onInit = func() {
		calls++
		observed = len(store.GetAll())
	}

	if err := store.Replace(makePodList(size), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Replace(makePodList(size), ""); err != nil {
		t.Fatal(err)
	}

	if calls != 1 {
		t.Errorf("onInit called %d times, want exactly 1", calls)
	}
	if observed != size {
		t.Errorf("onInit observed %d items, want the fully populated %d", observed, size)
	}
}
