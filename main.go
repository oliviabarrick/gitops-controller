package main

import (
	"github.com/justinbarrick/git-controller/pkg/reconciler"
	"github.com/justinbarrick/git-controller/pkg/util"
	snapshots "github.com/kubernetes-csi/external-snapshotter/pkg/apis/volumesnapshot/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"log"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
)

func main() {
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		Scheme: util.Scheme,
	})
	if err != nil {
		log.Fatal(err)
	}

	reconciler, err := reconciler.NewReconciler(mgr, "/tmp/myrepo")
	if err != nil {
		log.Fatal("cannot open repository:", err)
	}

	reconciler.SetClient(mgr.GetClient())

	if err := reconciler.Register(&snapshots.VolumeSnapshot{
		TypeMeta: metav1.TypeMeta{Kind: "VolumeSnapshot"},
	}, &snapshots.VolumeSnapshotContent{
		TypeMeta: metav1.TypeMeta{Kind: "VolumeSnapshotContent"},
	}); err != nil {
		log.Fatal("cannot initialize reconcilers:", err)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Fatal("cannot start manager:", err)
	}
}
