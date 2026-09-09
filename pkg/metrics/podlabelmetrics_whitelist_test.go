package metrics

import (
	"strings"
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectPodLabels drains a single scrape of the collector and returns the
// decoded label set of every emitted kube_pod_labels series.
func collectPodLabels(t *testing.T, kplc KubePodLabelsCollector) []map[string]string {
	t.Helper()

	ch := make(chan prometheus.Metric, 64)
	kplc.Collect(ch)
	close(ch)

	var series []map[string]string
	for metric := range ch {
		var m dto.Metric
		if err := metric.Write(&m); err != nil {
			t.Fatalf("Write: %v", err)
		}
		labels := make(map[string]string, len(m.Label))
		for _, l := range m.Label {
			labels[l.GetName()] = l.GetValue()
		}
		series = append(series, labels)
	}
	return series
}

func podLabelsCollector(useWhitelist bool) KubePodLabelsCollector {
	pods := []*clustercache.Pod{{
		Name:      "pod",
		Namespace: "ns",
		UID:       "pod-uid",
		Labels: map[string]string{
			"app":    "web",
			"secret": "drop-me",
		},
	}}

	return KubePodLabelsCollector{
		KubeClusterCache: &clustercache.MockClusterCache{Pods: pods},
		metricsConfig: MetricsConfig{
			UseLabelsWhitelist: useWhitelist,
			LabelsWhitelist:    map[string]bool{"app": true},
		},
	}
}

// TestPodLabelsCollectorEmitsOnlyWhitelistedLabels asserts the output of the
// whitelist filter, not just its construction: the emitted kube_pod_labels
// series must carry the whitelisted pod labels and no others. Inverting the
// filter condition keeps the whitelist and the cluster cache intact, so only
// the emitted series can catch it.
func TestPodLabelsCollectorEmitsOnlyWhitelistedLabels(t *testing.T) {
	series := collectPodLabels(t, podLabelsCollector(true))

	if len(series) != 1 {
		t.Fatalf("collected %d metrics, want 1 kube_pod_labels series", len(series))
	}
	labels := series[0]

	if got, ok := labels["label_app"]; !ok || got != "web" {
		t.Errorf("label_app = %q (present=%t), want \"web\"", got, ok)
	}
	if _, ok := labels["label_secret"]; ok {
		t.Errorf("emitted label_secret, want non-whitelisted pod labels filtered out")
	}
	for name := range labels {
		if strings.HasPrefix(name, "label_") && name != "label_app" {
			t.Errorf("emitted unexpected pod label %q, want only whitelisted labels", name)
		}
	}
}

// TestPodLabelsCollectorEmitsIdentityLabelsWhenWhitelisting asserts the
// filtered series still identifies its pod, so an implementation that drops
// everything cannot pass the whitelist assertion above.
func TestPodLabelsCollectorEmitsIdentityLabelsWhenWhitelisting(t *testing.T) {
	series := collectPodLabels(t, podLabelsCollector(true))

	if len(series) != 1 {
		t.Fatalf("collected %d metrics, want 1 kube_pod_labels series", len(series))
	}
	for name, want := range map[string]string{"pod": "pod", "namespace": "ns", "uid": "pod-uid"} {
		if got := series[0][name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestPodLabelsCollectorEmitsAllLabelsWithoutWhitelist asserts the filter only
// applies when UseLabelsWhitelist is set; with it off, every pod label is
// emitted including the one the whitelist would have dropped.
func TestPodLabelsCollectorEmitsAllLabelsWithoutWhitelist(t *testing.T) {
	series := collectPodLabels(t, podLabelsCollector(false))

	if len(series) != 1 {
		t.Fatalf("collected %d metrics, want 1 kube_pod_labels series", len(series))
	}
	labels := series[0]

	if got, ok := labels["label_secret"]; !ok || got != "drop-me" {
		t.Errorf("label_secret = %q (present=%t), want \"drop-me\" when whitelisting is off", got, ok)
	}
	if got, ok := labels["label_app"]; !ok || got != "web" {
		t.Errorf("label_app = %q (present=%t), want \"web\"", got, ok)
	}
}

// countingClusterCache wraps the mock cache and tallies the calls that the
// whitelist rebuild makes, so a test can assert how often the rebuild ran.
type countingClusterCache struct {
	*clustercache.MockClusterCache
	serviceCalls int
}

func (c *countingClusterCache) GetAllServices() []*clustercache.Service {
	c.serviceCalls++
	return c.MockClusterCache.GetAllServices()
}

// TestPodLabelsCollectorWhitelistIncludesControllerAndServiceSelectorKeys
// asserts the whitelist is derived from the cluster, not only from the static
// config: a label key used by a Service selector or a StatefulSet matchLabels
// must survive the copy-filter, while an unrelated label is dropped.
func TestPodLabelsCollectorWhitelistIncludesControllerAndServiceSelectorKeys(t *testing.T) {
	pods := []*clustercache.Pod{{
		Name: "pod", Namespace: "ns", UID: "pod-uid",
		Labels: map[string]string{"tier": "web", "role": "leader", "unrelated": "x"},
	}}
	kc := &clustercache.MockClusterCache{
		Pods:         pods,
		Services:     []*clustercache.Service{{SpecSelector: map[string]string{"tier": "web"}}},
		StatefulSets: []*clustercache.StatefulSet{{SpecSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"role": "leader"}}}},
	}
	kplc := KubePodLabelsCollector{
		KubeClusterCache: kc,
		metricsConfig:    MetricsConfig{UseLabelsWhitelist: true, LabelsWhitelist: map[string]bool{}},
	}

	series := collectPodLabels(t, kplc)
	if len(series) != 1 {
		t.Fatalf("collected %d metrics, want 1", len(series))
	}
	labels := series[0]
	if labels["label_tier"] != "web" {
		t.Errorf("label_tier = %q, want \"web\" (Service selector key must be whitelisted)", labels["label_tier"])
	}
	if labels["label_role"] != "leader" {
		t.Errorf("label_role = %q, want \"leader\" (StatefulSet matchLabels key must be whitelisted)", labels["label_role"])
	}
	if _, ok := labels["label_unrelated"]; ok {
		t.Errorf("emitted label_unrelated, want it filtered (no selector uses it)")
	}
}

// TestPodLabelsCollectorRebuildsWhitelistOncePerScrape guards the hoist: the
// whitelist (which walks every ReplicaSet, StatefulSet and Service in the
// cache) must be built once per Collect, not once per pod. On a cluster with
// tens of thousands of pods and owners the per-pod variant is O(pods × owners)
// on every Prometheus scrape.
func TestPodLabelsCollectorRebuildsWhitelistOncePerScrape(t *testing.T) {
	const podCount = 50
	pods := make([]*clustercache.Pod, 0, podCount)
	for i := 0; i < podCount; i++ {
		pods = append(pods, &clustercache.Pod{Name: "pod", Namespace: "ns", Labels: map[string]string{"app": "web"}})
	}
	kc := &countingClusterCache{MockClusterCache: &clustercache.MockClusterCache{
		Pods:     pods,
		Services: []*clustercache.Service{{SpecSelector: map[string]string{"app": "web"}}},
	}}
	kplc := KubePodLabelsCollector{
		KubeClusterCache: kc,
		metricsConfig:    MetricsConfig{UseLabelsWhitelist: true, LabelsWhitelist: map[string]bool{}},
	}

	series := collectPodLabels(t, kplc)
	if len(series) != podCount {
		t.Fatalf("collected %d series, want %d", len(series), podCount)
	}
	if kc.serviceCalls != 1 {
		t.Fatalf("whitelist rebuilt %d times for %d pods, want exactly 1 per scrape", kc.serviceCalls, podCount)
	}
}
