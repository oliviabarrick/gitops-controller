package util

import (
	"github.com/appscode/jsonpatch"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"path/filepath"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"strings"
)

var (
	Scheme    = runtime.NewScheme()
	defaulter = runtime.ObjectDefaulter(Scheme)
	Log       = logf.Log.WithName("gitops-controller")
)

func init() {
	logf.SetLogger(logf.ZapLogger(true))
	corev1.AddToScheme(Scheme)
}

// Return the metadata of an object.
func GetMeta(obj runtime.Object) metav1.Object {
	meta, _ := meta.Accessor(obj)
	return meta
}

// Get the Group Version Kind of an object.
func GetType(obj runtime.Object) schema.GroupVersionKind {
	return obj.GetObjectKind().GroupVersionKind()
}

func Kind(kind, group, version string) runtime.Object {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Kind:    kind,
		Group:   group,
		Version: version,
	})
	return obj
}

// Return an instantiated object of type kind that has name and namespace initialized
// to name.
func DefaultObject(kind runtime.Object, name, namespace string) runtime.Object {
	obj := kind.DeepCopyObject()
	meta := GetMeta(obj)
	meta.SetName(name)
	meta.SetNamespace(namespace)
	return obj
}

func MarshalObject(o runtime.Object, w io.Writer) error {
	encoder := json.NewYAMLSerializer(json.DefaultMetaFactory, nil, nil)

	meta := GetMeta(o)

	meta.SetResourceVersion("")
	meta.SetCreationTimestamp(metav1.Time{})
	meta.SetSelfLink("")
	meta.SetUID(types.UID(""))
	meta.SetGeneration(0)

	return encoder.Encode(o, w)
}

func PatchMatchesPath(patch jsonpatch.Operation, path string) (bool, error) {
	if patch.Path == path {
		return true, nil
	}

	rel, err := filepath.Rel(path, patch.Path)
	if err != nil {
		return false, err
	}
	if strings.HasPrefix(rel, "../") {
		return false, nil
	}

	return true, nil
}

// Check if a list contains a given string.
func Contains(list []string, key string) bool {
	for _, item := range list {
		if key == item {
			return true
		}
	}

	return len(list) == 0
}
