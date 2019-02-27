package main

import (
	"github.com/justinbarrick/gitops-controller/pkg/config"
	"github.com/justinbarrick/gitops-controller/pkg/reconciler"
	"github.com/justinbarrick/gitops-controller/pkg/util"
	"log"
	"os"
)

func main() {
	config, err := config.NewConfig("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	reconciler, err := reconciler.NewReconciler(config)
	if err != nil {
		util.Log.Error(err, "cannot open repository")
		os.Exit(1)
	}

	if err := reconciler.Start(); err != nil {
		util.Log.Error(err, "cannot start manager")
		os.Exit(1)
	}
}
