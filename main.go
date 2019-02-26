package main

import (
	"github.com/justinbarrick/git-controller/pkg/reconciler"
	"github.com/justinbarrick/git-controller/pkg/util"
	"os"
)


func main() {
	reconciler, err := reconciler.NewReconciler(os.Args[1], os.Args[2])
	if err != nil {
		util.Log.Error(err, "cannot open repository")
		os.Exit(1)
	}

	if err := reconciler.Start(); err != nil {
		util.Log.Error(err, "cannot start manager")
		os.Exit(1)
	}
}
