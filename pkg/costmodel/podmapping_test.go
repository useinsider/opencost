package costmodel

import (
	"fmt"
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
	"github.com/opencost/opencost/core/pkg/opencost"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildNamespacedCluster returns a mock cache with nsCount namespaces, each
// holding svcPerNS services, depPerNS deployments and podPerNS pods. Every
// service and deployment in a namespace selects app=<i> and every pod carries
// app=<i mod svcPerNS>, so each pod matches exactly one service and one
// deployment in its own namespace.
func buildNamespacedCluster(nsCount, svcPerNS, depPerNS, podPerNS int) *clustercache.MockClusterCache {
	kc := &clustercache.MockClusterCache{}
	for n := 0; n < nsCount; n++ {
		ns := fmt.Sprintf("ns-%d", n)
		for s := 0; s < svcPerNS; s++ {
			kc.Services = append(kc.Services, &clustercache.Service{
				Name:         fmt.Sprintf("svc-%d", s),
				Namespace:    ns,
				SpecSelector: map[string]string{"app": fmt.Sprintf("%d", s)},
			})
		}
		for d := 0; d < depPerNS; d++ {
			kc.Deployments = append(kc.Deployments, &clustercache.Deployment{
				Name:      fmt.Sprintf("dep-%d", d),
				Namespace: ns,
				SpecSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": fmt.Sprintf("%d", d)},
				},
			})
			kc.StatefulSets = append(kc.StatefulSets, &clustercache.StatefulSet{
				Name:      fmt.Sprintf("sts-%d", d),
				Namespace: ns,
				SpecSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": fmt.Sprintf("%d", d)},
				},
			})
		}
		for p := 0; p < podPerNS; p++ {
			kc.Pods = append(kc.Pods, &clustercache.Pod{
				Name:      fmt.Sprintf("pod-%d", p),
				Namespace: ns,
				Labels:    map[string]string{"app": fmt.Sprintf("%d", p%svcPerNS)},
			})
		}
	}
	return kc
}

func TestGetPodServicesMatchesOnlyWithinNamespace(t *testing.T) {
	kc := buildNamespacedCluster(3, 4, 4, 8)
	pods := kc.GetAllPods()

	got, err := getPodServices(kc, pods, "c1")
	if err != nil {
		t.Fatal(err)
	}

	for _, pod := range pods {
		key := pod.Namespace + ",c1"
		svcs := got[key][pod.Name]
		want := fmt.Sprintf("svc-%s", pod.Labels["app"])
		if len(svcs) != 1 || svcs[0] != want {
			t.Fatalf("pod %s/%s: got services %v, want [%s]", pod.Namespace, pod.Name, svcs, want)
		}
	}
	// every namespace key must exist even if it had no matching pods
	if len(got) != 3 {
		t.Fatalf("expected 3 namespace keys, got %d", len(got))
	}
}

func TestGetPodDeploymentsAndStatefulsetsMatchOnlyWithinNamespace(t *testing.T) {
	kc := buildNamespacedCluster(3, 4, 4, 8)
	pods := kc.GetAllPods()

	deps, err := getPodDeployments(kc, pods, "c1")
	if err != nil {
		t.Fatal(err)
	}
	stss, err := getPodStatefulsets(kc, pods, "c1")
	if err != nil {
		t.Fatal(err)
	}

	for _, pod := range pods {
		key := pod.Namespace + ",c1"
		if d := deps[key][pod.Name]; len(d) != 1 || d[0] != "dep-"+pod.Labels["app"] {
			t.Fatalf("pod %s/%s: deployments %v", pod.Namespace, pod.Name, d)
		}
		if s := stss[key][pod.Name]; len(s) != 1 || s[0] != "sts-"+pod.Labels["app"] {
			t.Fatalf("pod %s/%s: statefulsets %v", pod.Namespace, pod.Name, s)
		}
	}
}

// BenchmarkGetPodServices models a multi-tenant cluster where each namespace
// owns its own services and pods (e.g. one namespace per ephemeral test
// environment). 300 namespaces x 100 services x 100 pods = 30k services and
// 30k pods.
func BenchmarkGetPodServices(b *testing.B) {
	kc := buildNamespacedCluster(300, 100, 0, 100)
	pods := kc.GetAllPods()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := getPodServices(kc, pods, "c1"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetPodDeployments(b *testing.B) {
	kc := buildNamespacedCluster(300, 1, 100, 100)
	pods := kc.GetAllPods()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := getPodDeployments(kc, pods, "c1"); err != nil {
			b.Fatal(err)
		}
	}
}

// buildControllerLabels returns nsCount namespaces × perNS controllers, each
// selecting app=<i>, and nsCount × podPerNS pods carrying app=<i mod perNS>.
func buildControllerLabels(nsCount, perNS, podPerNS int) (map[podKey]map[string]string, map[controllerKey]map[string]string) {
	pods := map[podKey]map[string]string{}
	ctrls := map[controllerKey]map[string]string{}
	for n := 0; n < nsCount; n++ {
		ns := fmt.Sprintf("ns-%d", n)
		for c := 0; c < perNS; c++ {
			ctrls[newControllerKey("c1", ns, "deployment", fmt.Sprintf("dep-%d", c))] = map[string]string{"app": fmt.Sprintf("%d", c)}
		}
		for p := 0; p < podPerNS; p++ {
			pods[newPodKey("c1", ns, fmt.Sprintf("pod-%d", p))] = map[string]string{"app": fmt.Sprintf("%d", p%perNS)}
		}
	}
	return pods, ctrls
}

func TestLabelsToPodControllerMapMatchesWithinNamespaceOnly(t *testing.T) {
	pods, ctrls := buildControllerLabels(3, 4, 8)
	got := labelsToPodControllerMap(pods, ctrls)
	if len(got) != len(pods) {
		t.Fatalf("expected every pod matched, got %d of %d", len(got), len(pods))
	}
	for pKey, cKey := range got {
		if cKey.Namespace != pKey.Namespace || cKey.Cluster != pKey.Cluster {
			t.Fatalf("pod %v matched controller %v in another namespace", pKey, cKey)
		}
		if want := "dep-" + pods[pKey]["app"]; cKey.Controller != want {
			t.Fatalf("pod %v matched %s, want %s", pKey, cKey.Controller, want)
		}
	}
	// a pod in a namespace with no controllers stays unmatched
	pods[newPodKey("c1", "lonely", "pod-x")] = map[string]string{"app": "0"}
	if got := labelsToPodControllerMap(pods, ctrls); len(got) != len(pods)-1 {
		t.Fatalf("pod without a same-namespace controller must stay unmatched")
	}
}

func BenchmarkLabelsToPodControllerMap(b *testing.B) {
	pods, ctrls := buildControllerLabels(300, 70, 100) // 21k controllers, 30k pods
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		labelsToPodControllerMap(pods, ctrls)
	}
}

func TestApplyServicesToPodsMatchesWithinNamespaceOnly(t *testing.T) {
	podLabels, _ := buildControllerLabels(3, 4, 8)
	serviceLabels := map[serviceKey]map[string]string{}
	for n := 0; n < 3; n++ {
		for s := 0; s < 4; s++ {
			serviceLabels[newServiceKey("c1", fmt.Sprintf("ns-%d", n), fmt.Sprintf("svc-%d", s))] = map[string]string{"app": fmt.Sprintf("%d", s)}
		}
	}
	podMap := map[podKey]*pod{}
	for pKey := range podLabels {
		podMap[pKey] = &pod{Key: pKey, Allocations: map[string]*opencost.Allocation{"c": {Properties: &opencost.AllocationProperties{}}}}
	}
	allocsByService := map[serviceKey][]*opencost.Allocation{}
	applyServicesToPods(podMap, podLabels, allocsByService, serviceLabels)

	for pKey, p := range podMap {
		svcs := p.Allocations["c"].Properties.Services
		if len(svcs) != 1 || svcs[0] != "svc-"+podLabels[pKey]["app"] {
			t.Fatalf("pod %v: services %v", pKey, svcs)
		}
	}
	if len(allocsByService) != 12 {
		t.Fatalf("expected 12 services with allocations, got %d", len(allocsByService))
	}
}

func BenchmarkApplyServicesToPods(b *testing.B) {
	podLabels, _ := buildControllerLabels(300, 1, 100) // 30k pods
	serviceLabels := map[serviceKey]map[string]string{}
	for n := 0; n < 300; n++ {
		for s := 0; s < 90; s++ { // 27k services
			serviceLabels[newServiceKey("c1", fmt.Sprintf("ns-%d", n), fmt.Sprintf("svc-%d", s))] = map[string]string{"app": fmt.Sprintf("%d", s)}
		}
	}
	podMap := map[podKey]*pod{}
	for pKey := range podLabels {
		podMap[pKey] = &pod{Key: pKey, Allocations: map[string]*opencost.Allocation{"c": {Properties: &opencost.AllocationProperties{}}}}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		applyServicesToPods(podMap, podLabels, map[serviceKey][]*opencost.Allocation{}, serviceLabels)
	}
}
