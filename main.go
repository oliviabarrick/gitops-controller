package main

import (
	"github.com/justinbarrick/git-controller/pkg/reconciler"
	"github.com/justinbarrick/git-controller/pkg/util"
	"os"
)


func main() {
	workDir := "."
	if len(os.Args) > 2 {
		workDir = os.Args[2]
	}

	reconciler, err := reconciler.NewReconciler(os.Args[1], workDir)
	if err != nil {
		util.Log.Error(err, "cannot open repository")
		os.Exit(1)
	}

	if err := reconciler.Start(); err != nil {
		util.Log.Error(err, "cannot start manager")
		os.Exit(1)
	}
}
