package util

import (
	snapshots "github.com/kubernetes-csi/external-snapshotter/pkg/apis/volumesnapshot/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	Scheme    = runtime.NewScheme()
	defaulter = runtime.ObjectDefaulter(Scheme)
)

func init() {
	snapshots.AddToScheme(Scheme)
	corev1.AddToScheme(Scheme)
	extensions.AddToScheme(Scheme)
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
