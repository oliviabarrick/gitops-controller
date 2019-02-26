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
	"k8s.io/apimachinery/pkg/runtime"
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

// A rule that decides whether or not a resource should be synced, and whether it
// should be synced to Git or Kubernetes.
type Rule struct {
	// API groups to match the rule on. If empty, the rule matches all API groups.
	APIGroups []string `yaml:"apiGroups"`
	// Resource types to match the rule on. If empty, the rule matches any resources.
	Resources []string `yaml:"resources"`
	// Label selector to match the rule on. If empty, the rule matches any labels.
	Labels string `yaml:"labels"`
	// Which direction to sync resources. If syncTo is set to kubernetes, sync from
	// git to kubernetes. If syncTo is set to git, sync from kubernetes to git.
	SyncTo SyncType `yaml:"syncTo"`
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

// Configuration for the git-controller.
type Config struct {
	// Rules to load.
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

		obj := util.DefaultObject(kind, name, namespace)

		err := r.client.Get(context.TODO(), request.NamespacedName, obj)
		if err != nil && !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}

		k8sNotFound := errors.IsNotFound(err)

		found, err := r.repo.FindObjectInRepo(obj)
		if err != nil {
			return reconcile.Result{}, err
		}

		ruleFor := obj
		if k8sNotFound {
			ruleFor = found.Object
		}

		rule, err := r.config.RuleForObject(ruleFor)
		if err != nil {
			return reconcile.Result{}, err
		}

		if rule == nil {
			return reconcile.Result{}, nil
		}

		util.Log.Info("syncing", "kind", strKind, "name", name,
		              "namespace", namespace, "syncTo", rule.SyncTo)

		if rule.SyncTo == Git {
			if k8sNotFound {
				return reconcile.Result{}, r.repo.RemoveResource(obj, found)
			}

			return reconcile.Result{}, r.repo.AddResource(obj, found)
		} else {
			if k8sNotFound && found == nil {
				return reconcile.Result{}, nil
			} else if k8sNotFound {
				util.Log.Info("recreating object from git", "kind", strKind, "name",
				              name, "namespace", namespace)
				return reconcile.Result{}, r.client.Create(context.TODO(), found.Object)
			}

			if found == nil {
				util.Log.Info("deleting object not in git", "kind", strKind, "name",
							  name, "namespace", namespace)
				err = r.client.Delete(context.TODO(), obj)
				if err != nil && ! errors.IsNotFound(err) {
					return reconcile.Result{}, err
				}
				return reconcile.Result{}, nil
			}

			util.Log.Info("restoring object to git state", "kind", strKind, "name",
						  name, "namespace", namespace)
			return reconcile.Result{}, r.client.Update(context.TODO(), found.Object)
		}

		return reconcile.Result{}, nil
	})
}

func (r *Reconciler) RegisterReconcilerForType(kind runtime.Object) error {
	strKind := kind.GetObjectKind().GroupVersionKind().Kind
	name := fmt.Sprintf("%s-controller", strKind)
	util.Log.Info("starting controller", "kind", strKind)

	ctrlr, err := controller.New(name, r.mgr, controller.Options{
		Reconciler: r.ReconcilerForType(kind),
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
