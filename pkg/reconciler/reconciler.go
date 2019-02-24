package reconciler

import (
	"context"
	"fmt"
	"github.com/justinbarrick/git-controller/pkg/repo"
	"github.com/justinbarrick/git-controller/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Reconciler that synchronizes objects in Kubernetes to a git repository.
type Reconciler struct {
	client  client.Client
	repo    *repo.Repo
	mgr     manager.Manager
	repoDir string
}

// Create a new reconciler and checkout the repository.
func NewReconciler(repoDir string, manifestsPath string) (*Reconciler, error) {
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		Scheme: util.Scheme,
	})
	if err != nil {
		return nil, err
	}

	repo, err := repo.NewRepo(repoDir, manifestsPath)
	if err != nil {
		return nil, err
	}

	return &Reconciler{
		repo:   repo,
		mgr:    mgr,
		client: mgr.GetClient(),
	}, nil
}

// Register the reconciler for each prototype object provided.
func (r *Reconciler) Register(kinds ...runtime.Object) error {
	for _, kind := range kinds {
		if err := r.ReconcilerForType(kind); err != nil {
			return err
		}
	}

	return nil
}

// Return an instantiated object of type kind that has name and namespace initialized
// to name.
func (r *Reconciler) DefaultObject(kind runtime.Object, name types.NamespacedName) runtime.Object {
	obj := kind.DeepCopyObject()
	util.GetMeta(obj).SetName(name.Name)
	util.GetMeta(obj).SetNamespace(name.Namespace)
	return obj
}

// Create a reconciler for the provided type that checks each object against its
// definition in git.
func (r *Reconciler) ReconcilerForType(kind runtime.Object) error {
	strKind := kind.GetObjectKind().GroupVersionKind().Kind
	name := fmt.Sprintf("%s-controller", strKind)
	util.Log.Info("starting controller", "kind", strKind)

	rec := reconcile.Func(func(request reconcile.Request) (reconcile.Result, error) {
		obj := r.DefaultObject(kind, request.NamespacedName)

		err := r.client.Get(context.TODO(), request.NamespacedName, obj)
		if err != nil && !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		} else if err != nil && errors.IsNotFound(err) {
			return reconcile.Result{}, r.repo.RemoveResource(obj)
		}

		return reconcile.Result{}, r.repo.AddResource(obj)
	})

	ctrlr, err := controller.New(name, r.mgr, controller.Options{
		Reconciler: rec,
	})
	if err != nil {
		return err
	}

	return ctrlr.Watch(&source.Kind{
		Type: kind,
	}, &handler.EnqueueRequestForObject{})
}

func (r *Reconciler) Start() error {
	return r.mgr.Start(signals.SetupSignalHandler())
}
