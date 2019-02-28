package util

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"testing"
)

func TestMarshalObject(t *testing.T) {
	dep := DefaultObject(Kind("Deployment", "extensions", "v1beta1"), "name", "default")
	asUnstructured := dep.(*unstructured.Unstructured)
	asUnstructured.Object["status"] = map[string]interface{}{"status": "ok"}

	meta := GetMeta(asUnstructured)
	meta.SetAnnotations(map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": "hello world",
		"my": "annotation",
	})

	buf := &bytes.Buffer{}

	assert.Nil(t, MarshalObject(asUnstructured, buf))

	obj := &unstructured.Unstructured{}
	decoder := yaml.NewYAMLOrJSONDecoder(buf, len(buf.Bytes()))
	assert.Nil(t, decoder.Decode(obj))

	meta = GetMeta(obj)
	kind := GetType(obj)
	assert.Equal(t, map[string]string{"my": "annotation"}, meta.GetAnnotations())
	assert.Equal(t, nil, obj.Object["status"])
	assert.Equal(t, "name", meta.GetName())
	assert.Equal(t, "default", meta.GetNamespace())
	assert.Equal(t, "Deployment", kind.Kind)
}
