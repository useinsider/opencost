package metrics

import (
	"sync"
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/labels"
)

// TestPodLabelsCollectorDoesNotMutateCache guards against the collector
// deleting non-whitelisted keys from the pod.Labels map that the cluster
// cache hands out. With the V2 GenericStore that map is shared with every
// other reader (cost model, other collectors), so an in-place delete is both
// a data race (see opencost/opencost#2910) and a silent corruption of the
// cached pod. Run with -race.
func TestPodLabelsCollectorDoesNotMutateCache(t *testing.T) {
	const podCount = 200

	pods := make([]*clustercache.Pod, 0, podCount)
	for i := 0; i < podCount; i++ {
		pods = append(pods, &clustercache.Pod{
			Name:      "pod",
			Namespace: "ns",
			Labels: map[string]string{
				"app":        "web",
				"not-listed": "drop-me",
			},
		})
	}

	kc := &clustercache.MockClusterCache{Pods: pods}
	kplc := KubePodLabelsCollector{
		KubeClusterCache: kc,
		metricsConfig: MetricsConfig{
			UseLabelsWhitelist: true,
			LabelsWhitelist:    map[string]bool{"app": true},
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: the prometheus scrape path.
	go func() {
		defer wg.Done()
		ch := make(chan prometheus.Metric, 16)
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		kplc.Collect(ch)
		close(ch)
		<-done
	}()

	// Reader: what getPodServices does on every emitter tick.
	go func() {
		defer wg.Done()
		sel := labels.Set(map[string]string{"app": "web"}).AsSelectorPreValidated()
		for _, p := range kc.GetAllPods() {
			_ = sel.Matches(labels.Set(p.Labels))
		}
	}()

	wg.Wait()

	for _, p := range kc.GetAllPods() {
		if _, ok := p.Labels["not-listed"]; !ok {
			t.Fatalf("collector removed a label from the cached pod; the cache must not be mutated by a scrape")
		}
	}
}
