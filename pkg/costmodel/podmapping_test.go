package costmodel

import (
	"fmt"
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
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
