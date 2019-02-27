package reconciler

import (
	"context"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/justinbarrick/gitops-controller/pkg/config"
	"github.com/justinbarrick/gitops-controller/pkg/repo"
	"github.com/justinbarrick/gitops-controller/pkg/util"
	ryaml "github.com/justinbarrick/gitops-controller/pkg/yaml"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
	k8sconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"strings"
	"time"
)

type Source struct {
	Kind runtime.Object
	Chan chan event.GenericEvent
}

// Reconciler that synchronizes objects in Kubernetes to a git repository.
type Reconciler struct {
	config  *config.Config
	client  client.Client
	repo    *repo.Repo
	mgr     manager.Manager
	sources []Source
}

// Create a new reconciler and checkout the repository.
func NewReconciler(config *config.Config) (*Reconciler, error) {
	mgr, err := manager.New(k8sconfig.GetConfigOrDie(), manager.Options{
		Scheme: util.Scheme,
	})
	if err != nil {
		return nil, err
	}

	repo, err := repo.NewRepo(config.GitURL, config.GitPath)
	if err != nil {
		return nil, err
	}

	r := &Reconciler{
		config:  config,
		repo:    repo,
		mgr:     mgr,
		client:  mgr.GetClient(),
		sources: []Source{},
	}

	dClient := discovery.NewDiscoveryClientForConfigOrDie(mgr.GetConfig())
	resourceTypes, err := dClient.ServerPreferredResources()
	for _, resourceType := range resourceTypes {
		for _, resource := range resourceType.APIResources {
			group := ""
			version := ""

			splitVersion := strings.Split(resourceType.GroupVersion, "/")
			if len(splitVersion) == 1 {
				version = splitVersion[0]
			} else {
				version = splitVersion[1]
				group = splitVersion[0]
			}

			hasRequiredVerbs := true
			for _, verb := range []string{"watch", "list", "get", "update", "delete"} {
				if !util.Contains(resource.Verbs, verb) {
					hasRequiredVerbs = false
				}
			}

			if !hasRequiredVerbs || len(resource.Verbs) == 0 {
				continue
			}

			if resource.Kind == "ReplicationControllerDummy" {
				spew.Dump(resource)
			}

			if err := r.Register(util.Kind(resource.Kind, group, version)); err != nil {
				return nil, err
			}
		}
	}

	return r, nil
}

// Register the reconciler for each prototype object provided.
func (r *Reconciler) Register(kinds ...runtime.Object) error {
	for _, kind := range kinds {
		if err := r.RegisterReconcilerForType(kind); err != nil {
			return err
		}
	}

	return nil
}

// Create a reconciler for the provided type that checks each object against its
// definition in git.
func (r *Reconciler) ReconcilerForType(kind runtime.Object) reconcile.Func {
	return reconcile.Func(func(request reconcile.Request) (reconcile.Result, error) {
		strKind := kind.GetObjectKind().GroupVersionKind().Kind
		name := request.NamespacedName.Name
		namespace := request.NamespacedName.Namespace

		k8sState := util.DefaultObject(kind, name, namespace)

		// Required operations:
		// 1. Check Kubernetes
		// 2. Check Git
		// 3. If the resource does not exist in either place, return.
		// 4. If the resource does not exist in Git and SyncTo is Kubernetes, delete from Kubernetes.
		// 5. If the resource does not exist in Git and SyncTo is Git, add to Git.
		// 6. If the resource does not exist in Kubernetes and SyncTo is Git, delete from Git.
		// 7. If the resource does not exist in Kubernetes and SyncTo is Kubernetes, add to Kubernetes.
		// 8. If the resources are out of sync and SyncTo is Git, update Git.
		// 9. If the resources are out of sync and SyncTo is Kubernetes, update Kubernetes.

		// Fetch resource from Kubernetes
		err := r.client.Get(context.TODO(), request.NamespacedName, k8sState)
		if err != nil && !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		k8sNotFound := errors.IsNotFound(err)

		// Fetch resource from Git.
		gitState, err := r.repo.FindObjectInRepo(k8sState)
		if err != nil {
			return reconcile.Result{}, err
		}

		if k8sNotFound {
			k8sState = nil
		}

		// If the resource does not exist in either place, return.
		if k8sState == nil && gitState == nil {
			return reconcile.Result{}, nil
		}

		var gitStateObj runtime.Object
		if gitState != nil {
			gitStateObj = gitState.Object
		}

		// Get a rule that matches the object.
		rule, err := r.config.RuleForObject(k8sState, gitStateObj)
		if err != nil {
			return reconcile.Result{}, err
		}

		// If no rules match, return.
		if rule == nil {
			return reconcile.Result{}, nil
		}

		// Check if there are no changes to sync.
		if gitStateObj != nil && k8sState != nil {
			patch := admission.PatchResponse(k8sState, gitStateObj)
			if len(patch.Patches) == 0 {
				return reconcile.Result{}, nil
			}
		}

		// Synchronize to Git or Kubernetes, depending on the SyncTo type of the rule.
		util.Log.Info("syncing", "kind", strKind, "name", name,
			"namespace", namespace, "syncTo", rule.SyncTo)

		if rule.SyncTo == config.Git {
			err = r.SyncObjectToGit(k8sState, gitState, rule)
		} else {
			err = r.SyncObjectToKubernetes(k8sState, gitState, rule)
		}

		return reconcile.Result{}, err
	})
}

func (r *Reconciler) SyncObjectToGit(k8sState runtime.Object, gitState *ryaml.Object, rule *config.Rule) error {
	var err error

	if k8sState == nil {
		err = r.repo.RemoveResource(k8sState, gitState)
	} else {
		if gitState != nil {
			k8sState, err = config.PatchObject(gitState.Object, k8sState, rule)
			if err != nil {
				return err
			}
		}

		err = r.repo.AddResource(k8sState, gitState)
	}

	if err != nil {
		return err
	}

	return r.repo.Push()
}

func (r *Reconciler) SyncObjectToKubernetes(k8sState runtime.Object, gitState *ryaml.Object, rule *config.Rule) error {
	if k8sState == nil && gitState == nil {
		return nil
	}

	var logMeta metav1.Object
	var kind string
	if k8sState != nil {
		logMeta = util.GetMeta(k8sState)
		kind = util.GetType(k8sState).Kind
	} else {
		logMeta = util.GetMeta(gitState.Object)
		kind = util.GetType(gitState.Object).Kind
	}

	if gitState == nil {
		util.Log.Info("deleting object not in git", "kind", kind, "name",
			logMeta.GetName(), "namespace", logMeta.GetNamespace())
		if err := r.client.Delete(context.TODO(), k8sState); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}

	if k8sState == nil {
		util.Log.Info("recreating object from git", "kind", kind, "name",
			logMeta.GetName(), "namespace", logMeta.GetNamespace())
		return r.client.Create(context.TODO(), gitState.Object)
	}

	util.Log.Info("restoring object to git state", "kind", kind, "name",
		logMeta.GetName(), "namespace", logMeta.GetNamespace())

	patched, err := config.PatchObject(k8sState, gitState.Object, rule)
	if err != nil {
		return err
	}

	return r.client.Update(context.TODO(), patched)
}

func (r *Reconciler) RegisterReconcilerForType(kind runtime.Object) error {
	gvk := kind.GetObjectKind().GroupVersionKind()
	strKind := gvk.Kind
	name := fmt.Sprintf("git:%s/%s:%s", gvk.Group, gvk.Version, strKind)
	util.Log.Info("starting controller", "kind", strKind, "name", name)

	reconciler := r.ReconcilerForType(kind)

	ctrlr, err := controller.New(name, r.mgr, controller.Options{
		Reconciler: reconciler,
	})
	if err != nil {
		return err
	}

	events := make(chan event.GenericEvent)
	r.sources = append(r.sources, Source{
		Kind: kind,
		Chan: events,
	})

	if err := ctrlr.Watch(
		&source.Channel{Source: events},
		&handler.EnqueueRequestForObject{},
	); err != nil {
		return err
	}

	return ctrlr.Watch(&source.Kind{
		Type: kind,
	}, &handler.EnqueueRequestForObject{})
}

func (r *Reconciler) GitSync() error {
	if err := r.repo.Pull(); err != nil {
		return err
	}

	objects, err := r.repo.LoadRepoYAMLs()
	if err != nil {
		return err
	}

	for _, obj := range objects {
		kind := util.GetType(obj.Object)
		meta := util.GetMeta(obj.Object)

		for _, source := range r.sources {
			sourceKind := util.GetType(source.Kind)
			if sourceKind.Kind != kind.Kind || sourceKind.Group != kind.Group {
				continue
			}

			source.Chan <- event.GenericEvent{
				Meta:   meta,
				Object: obj.Object,
			}
		}
	}

	return nil
}

func (r *Reconciler) Start() error {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for _ = range ticker.C {
			util.Log.Info("resyncing")
			r.GitSync()
		}
	}()
	return r.mgr.Start(signals.SetupSignalHandler())
}
