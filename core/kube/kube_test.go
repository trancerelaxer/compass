package kube

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestK8s() *K8s {
	k := K8s{}
	k.client = fake.NewSimpleClientset()
	return &k
}

func createFakeNamespace(k8s *K8s, name string) error {
	n := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}}
	_, err := k8s.client.Core().Namespaces().Create(n)
	return err
}

func TestFromConfigMap(t *testing.T) {
	k8s := newTestK8s()
	namespace := "kube-system"
	err := createFakeNamespace(k8s, namespace)
	assert.NoError(t, err)

	testData := map[string]string{"test": "data"}
	c := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test-cm"}, Data: testData}
	_, err = k8s.client.Core().ConfigMaps(namespace).Create(c)
	assert.NoError(t, err)

	result, err := k8s.FromConfigMap("test-cm", namespace, "test")
	assert.NoError(t, err)
	assert.Equal(t, testData["test"], result)

	result, err = k8s.FromConfigMap("not-exist", namespace, "test")
	assert.Errorf(t, err, "failed to get configmap")
	assert.Empty(t, result)
}

func TestFromSecret(t *testing.T) {
	k8s := newTestK8s()
	namespace := "kube-system"
	err := createFakeNamespace(k8s, namespace)
	assert.NoError(t, err)

	testData := map[string][]byte{"test": []byte("data")}
	s := &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "test-sec"}, Data: testData}
	_, err = k8s.client.Core().Secrets(namespace).Create(s)
	assert.NoError(t, err)

	result, err := k8s.FromSecret("test-sec", namespace, "test")
	assert.NoError(t, err)
	assert.Equal(t, "data", result)

	result, err = k8s.FromSecret("not-exist", namespace, "test")
	assert.Errorf(t, err, "failed to get secret")
	assert.Empty(t, result)
}

func TestFindTiller(t *testing.T) {
	k8s := newTestK8s()

	pod, err := k8s.FindPod("tiller", "kube-system")
	assert.Errorf(t, err, "tiller not found")

	namespace := "kube-system"
	err = createFakeNamespace(k8s, namespace)
	assert.NoError(t, err)

	p := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "tiller-test", Labels: map[string]string{"name": "tiller"}}}
	_, err = k8s.client.Core().Pods(namespace).Create(p)
	if err != nil {
		t.Errorf("Error injecting pod into fake client: %v", err)
	}

	pod, err = k8s.FindPod("tiller", namespace)
	assert.Equal(t, "tiller-test", pod)
}
