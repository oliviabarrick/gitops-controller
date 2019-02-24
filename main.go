package main

import (
	"github.com/justinbarrick/git-controller/pkg/reconciler"
	snapshots "github.com/kubernetes-csi/external-snapshotter/pkg/apis/volumesnapshot/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
)

func main() {
	reconciler, err := reconciler.NewReconciler("/tmp/myrepo")
	if err != nil {
		log.Fatal("cannot open repository:", err)
	}

	if err := reconciler.Register(&snapshots.VolumeSnapshot{
		TypeMeta: metav1.TypeMeta{Kind: "VolumeSnapshot"},
	}, &snapshots.VolumeSnapshotContent{
		TypeMeta: metav1.TypeMeta{Kind: "VolumeSnapshotContent"},
	}); err != nil {
		log.Fatal("cannot initialize reconcilers:", err)
	}

	if err := reconciler.Start(); err != nil {
		log.Fatal("cannot start manager:", err)
	}
}
