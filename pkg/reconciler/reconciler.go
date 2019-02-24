package reconciler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"gopkg.in/yaml.v2"
	"github.com/jinzhu/inflection"
	"github.com/justinbarrick/git-controller/pkg/repo"
	"github.com/justinbarrick/git-controller/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type SyncType string

const (
	Kubernetes SyncType = "kubernetes"
	Git SyncType = "git"
)

// Check if a list contains a given string.
func contains(list []string, key string) bool {
	for _, item := range list {
		if key == item {
			return true
		}
	}

	return len(list) == 0
}

type Rule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Labels string `yaml:"labels"`
	Filters []string `yaml:"filters"`
	Sync SyncType `yaml:"sync"`
}

// Return the rule's action.
func (r *Rule) Action() SyncType {
	return r.Sync
}

// Return the normalized version of the list of resources
func (r *Rule) NormalizedResources() []string {
	resources := []string{}

	for _, resource := range r.Resources {
		resources = append(resources, strings.ToLower(inflection.Singular(resource)))
	}

	return resources
}

// Check if an object is matched by a rule.
func (r *Rule) Matches(obj runtime.Object) (bool, error) {
	kind := util.GetType(obj)
	meta := util.GetMeta(obj)

	if ! contains(r.APIGroups, kind.Group) {
		return false, nil
	}

	if ! contains(r.NormalizedResources(), strings.ToLower(kind.Kind)) {
		return false, nil
	}

	if r.Labels != "" {
		labelSelector, err := labels.Parse(r.Labels)
		if err != nil {
			return false, err
		}

		if ! labelSelector.Matches(labels.Set(meta.GetLabels())) {
			return false, nil
		}
	}

	return true, nil
}

type Config struct {
	Rules []Rule `yaml:"rules"`
}

func NewConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer file.Close()

	config := &Config{}

	if err := yaml.NewDecoder(file).Decode(config); err != nil {
		return nil, err
	}

	return config, nil
}

func (c *Config) RuleForObject(obj runtime.Object) (*Rule, error) {
	for _, rule := range c.Rules {
		match, err := rule.Matches(obj)
		if err != nil {
			return nil, err
		}

		if match {
			return &rule, nil
		}
	}

	return nil, nil
}

// Reconciler that synchronizes objects in Kubernetes to a git repository.
type Reconciler struct {
	config  *Config
	client  client.Client
	repo    *repo.Repo
	mgr     manager.Manager
	restMap meta.RESTMapper
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

	restMap, err := apiutil.NewDiscoveryRESTMapper(mgr.GetConfig())
	if err != nil {
		return nil, err
	}

	config, err := NewConfig("config.yaml")
	if err != nil {
		return nil, err
	}

	return &Reconciler{
		config: config,
		repo:   repo,
		mgr:    mgr,
		restMap: restMap,
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

		rule, err := r.config.RuleForObject(obj)
		if err != nil {
			return reconcile.Result{}, err
		}

		if rule == nil {
			return reconcile.Result{}, nil
		}

		util.Log.Info("syncing", "kind", strKind, "name", request.NamespacedName.Name,
		              "namespace", request.NamespacedName.Namespace,
		              "sync", rule.Action())

		err = r.client.Get(context.TODO(), request.NamespacedName, obj)
		if err != nil && !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		if rule.Action() == Git {
			if errors.IsNotFound(err) {
				return reconcile.Result{}, r.repo.RemoveResource(obj)
			}

			return reconcile.Result{}, r.repo.AddResource(obj)
		} else {
			found, err := r.repo.FindObjectInRepo(obj)
			if err != nil {
				return reconcile.Result{}, err
			}

			if errors.IsNotFound(err) {
				if found != nil {
					util.Log.Info("recreating object from git", "kind", strKind, "name",
					              request.NamespacedName.Name, "namespace",
					              request.NamespacedName.Namespace)
					return reconcile.Result{}, r.client.Create(context.TODO(), found.Object)
				}

				return reconcile.Result{}, nil
			}

			if found == nil {
				util.Log.Info("deleting object not in git", "kind", strKind, "name",
							  request.NamespacedName.Name, "namespace",
							  request.NamespacedName.Namespace)
				err = r.client.Delete(context.TODO(), obj)
				if err != nil && ! errors.IsNotFound(err) {
					return reconcile.Result{}, err
				}
				return reconcile.Result{}, nil
			}

			util.Log.Info("restoring object to git state", "kind", strKind, "name",
						  request.NamespacedName.Name, "namespace",
						  request.NamespacedName.Namespace)

			meta := util.GetMeta(found.Object)
			apiMeta := util.GetMeta(obj)
			meta.SetResourceVersion(apiMeta.GetResourceVersion())

			if equality.Semantic.DeepEqual(found.Object, obj) {
				return reconcile.Result{}, nil
			}

			return reconcile.Result{}, r.client.Update(context.TODO(), found.Object)
		}

		return reconcile.Result{}, nil
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
