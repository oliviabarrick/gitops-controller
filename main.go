package main

import (
	"github.com/justinbarrick/git-controller/pkg/reconciler"
	"github.com/justinbarrick/git-controller/pkg/util"
	"fmt"
	"log"
	"os"
)


func main() {
	if len(os.Args) < 2 {
		log.Fatal(fmt.Sprintf("Usage: %s <git URL> [working directory]", os.Args[0]))
	}

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
