package reconciler

import (
	"github.com/justinbarrick/git-controller/pkg/yaml"
	"github.com/justinbarrick/git-controller/pkg/repo"
	"fmt"
	"path/filepath"
	"log"
	"context"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sync"
)

const (
	webhookNamespace = "git-controller"
	webhookName      = "git-controller"
)

type Reconciler struct {
	lock sync.Mutex
	client  client.Client
	repo *repo.Repo
	mgr manager.Manager
	repoDir string
}

func NewReconciler(mgr manager.Manager, repoDir string) (*Reconciler, error) {
	repo, err := repo.NewRepo(repoDir)
	if err != nil {
		return nil, err
	}

	return &Reconciler{
		repo: repo,
		mgr: mgr,
	}, nil
}

// Set the Kubernetes client.
func (r *Reconciler) SetClient(client client.Client) {
	r.client = client
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

// Create a reconciler for the provided type that checks each object against its
// definition in git.
func (r *Reconciler) ReconcilerForType(kind runtime.Object) error {
	strKind := kind.GetObjectKind().GroupVersionKind().Kind
	name := fmt.Sprintf("%s-controller", strKind)
	log.Printf("Starting controller for %s", strKind)

	rec := reconcile.Func(func(request reconcile.Request) (reconcile.Result, error) {
		r.lock.Lock()
		defer r.lock.Unlock()

		log.Println("Reconciling:", request.NamespacedName)

		obj := kind.DeepCopyObject()
		yaml.GetMeta(obj).SetName(request.NamespacedName.Name)
		yaml.GetMeta(obj).SetNamespace(request.NamespacedName.Namespace)

		err := r.client.Get(context.TODO(), request.NamespacedName, obj)
		if err != nil && ! errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		resource, err := r.repo.FindObjectInRepo(obj)
		if err != nil {
			return reconcile.Result{}, err
		}

		if resource == nil {
			resource = &yaml.ObjectMapping{}
		}

		resource.Object = obj

		if err := resource.Save(); err != nil {
			return reconcile.Result{}, err
		}

		gitPath, err := filepath.Rel("/tmp/myrepo/", resource.File.Path)
		if err != nil {
			return reconcile.Result{}, err
		}

		if err = r.repo.Add(gitPath); err != nil {
			return reconcile.Result{}, err
		}

		commit := fmt.Sprintf("Updating %s", request.NamespacedName.String())
		return reconcile.Result{}, r.repo.Commit(commit)
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
