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
