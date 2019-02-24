package main

import (
	"github.com/justinbarrick/git-controller/pkg/reconciler"
	"github.com/justinbarrick/git-controller/pkg/util"
	"log"
)

func main() {
	reconciler, err := reconciler.NewReconciler("/tmp/myrepo")
	if err != nil {
		log.Fatal("cannot open repository:", err)
	}

	if err := reconciler.Register(
		util.Kind("VolumeSnapshot", "snapshot.storage.k8s.io", "v1alpha1"),
		util.Kind("VolumeSnapshotContent", "snapshot.storage.k8s.io", "v1alpha1"),
		util.Kind("Deployment", "extensions", "v1beta1"),
	); err != nil {
		log.Fatal("cannot initialize reconcilers:", err)
	}

	if err := reconciler.Start(); err != nil {
		log.Fatal("cannot start manager:", err)
	}
}
