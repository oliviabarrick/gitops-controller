package util

import (
	extensions "k8s.io/api/extensions/v1beta1"
	corev1 "k8s.io/api/core/v1"
	snapshots "github.com/kubernetes-csi/external-snapshotter/pkg/apis/volumesnapshot/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	Scheme = runtime.NewScheme()
	defaulter = runtime.ObjectDefaulter(Scheme)
)

func init() {
	snapshots.AddToScheme(Scheme)
	corev1.AddToScheme(Scheme)
	extensions.AddToScheme(Scheme)
}
