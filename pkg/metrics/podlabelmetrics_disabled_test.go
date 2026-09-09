package metrics

import (
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectByType drains a single scrape and tallies the emitted metrics by
// concrete type. Unlike collectPodLabels it keeps the metric type, which is
// what distinguishes a suppressed kube_pod_labels series from a suppressed
// scrape.
func collectByType(t *testing.T, kplc KubePodLabelsCollector) (labelSeries int, ownerSeries int) {
	t.Helper()

	ch := make(chan prometheus.Metric, 64)
	kplc.Collect(ch)
	close(ch)

	for metric := range ch {
		switch metric.(type) {
		case KubePodLabelsMetric:
			labelSeries++
		case KubePodOwnerMetric:
			ownerSeries++
		}
	}
	return labelSeries, ownerSeries
}

// disabledPodLabelsCache builds a cluster cache whose single pod carries both
// labels and an owner reference, plus a Service whose selector would feed the
// whitelist rebuild.
func disabledPodLabelsCache() *clustercache.MockClusterCache {
	isController := true
	return &clustercache.MockClusterCache{
		Pods: []*clustercache.Pod{{
			Name:      "pod",
			Namespace: "ns",
			UID:       "pod-uid",
			Labels:    map[string]string{"app": "web"},
			OwnerReferences: []metav1.OwnerReference{{
				Name:       "rs",
				Kind:       "ReplicaSet",
				Controller: &isController,
			}},
		}},
		Services: []*clustercache.Service{{SpecSelector: map[string]string{"app": "web"}}},
	}
}

// TestPodLabelsCollectorEmitsNoPodLabelsSeriesWhenDisabled asserts the
// disabled-metric guard still suppresses kube_pod_labels once whitelisting is
// on. kube_pod_owner must still be emitted, so a collector that short-circuits
// the whole scrape cannot pass by emitting nothing.
func TestPodLabelsCollectorEmitsNoPodLabelsSeriesWhenDisabled(t *testing.T) {
	kplc := KubePodLabelsCollector{
		KubeClusterCache: disabledPodLabelsCache(),
		metricsConfig: MetricsConfig{
			DisabledMetrics:    []string{"kube_pod_labels"},
			UseLabelsWhitelist: true,
			LabelsWhitelist:    map[string]bool{"app": true},
		},
	}

	labelSeries, ownerSeries := collectByType(t, kplc)

	if labelSeries != 0 {
		t.Errorf("emitted %d kube_pod_labels series, want 0 when the metric is disabled", labelSeries)
	}
	if ownerSeries != 1 {
		t.Errorf("emitted %d kube_pod_owner series, want 1 (only kube_pod_labels is disabled)", ownerSeries)
	}
}

// TestPodLabelsCollectorDoesNotRebuildWhitelistWhenPodLabelsDisabled guards
// the interaction between the hoist and the disabled-metric guard: the
// whitelist exists only to filter kube_pod_labels, so when that metric is
// disabled the rebuild (a walk of every ReplicaSet, StatefulSet and Service in
// the cache) must not run at all. countingClusterCache is declared in
// podlabelmetrics_whitelist_test.go.
func TestPodLabelsCollectorDoesNotRebuildWhitelistWhenPodLabelsDisabled(t *testing.T) {
	kc := &countingClusterCache{MockClusterCache: disabledPodLabelsCache()}
	kplc := KubePodLabelsCollector{
		KubeClusterCache: kc,
		metricsConfig: MetricsConfig{
			DisabledMetrics:    []string{"kube_pod_labels"},
			UseLabelsWhitelist: true,
			LabelsWhitelist:    map[string]bool{"app": true},
		},
	}

	collectByType(t, kplc)

	if kc.serviceCalls != 0 {
		t.Errorf("whitelist rebuilt %d times, want 0 when kube_pod_labels is disabled", kc.serviceCalls)
	}
}
