package reconciler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"gopkg.in/yaml.v2"
	"github.com/jinzhu/inflection"
	"github.com/justinbarrick/git-controller/pkg/repo"
	"github.com/justinbarrick/git-controller/pkg/util"
	ryaml "github.com/justinbarrick/git-controller/pkg/yaml"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
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
//
// Decision tree to determine if resource matches a rule:
// 1. If resource kind is not included in the rule's resources and the rule has a resources argument, rule does not match.
// 2. If resource group is not included in the rule's groups and the rule has a groups argument, rule does not match.
// 3. If labels are not set in Git and SyncTo is Kubernetes, rule does not match.
// 4. If labels are not set in Kubernetes and SyncTo is Git, rule does not match.
// 5. Rule matches.
func (r *Rule) Matches(k8sState runtime.Object, gitState runtime.Object) (bool, error) {
	var obj runtime.Object
	if k8sState != nil {
		obj = k8sState
	} else {
		obj = gitState
	}

	kind := util.GetType(obj)

	if ! contains(r.NormalizedResources(), strings.ToLower(kind.Kind)) {
		return false, nil
	}

	if ! contains(r.APIGroups, kind.Group) {
		return false, nil
	}

	if r.Labels != "" {
		labelSelector, err := labels.Parse(r.Labels)
		if err != nil {
			return false, err
		}

		if r.SyncTo == Kubernetes {
			obj = gitState
		} else if r.SyncTo == Git {
			obj = k8sState
		}

		if obj == nil {
			return false, nil
		}

		objLabels := util.GetMeta(obj).GetLabels()

		if ! labelSelector.Matches(labels.Set(objLabels)) {
			return false, nil
		}
	}

	return true, nil
}

// A resource kind to load in the controller..
type Kind struct {
	// The kind of the resource (e.g., Deployment).
	Kind string `yaml:"kind"`
	// The group of the resource (e.g., extensions).
	Group string `yaml:"group"`
	// The apiVersion of the resource (e.g., v1beta1).
	APIVersion string `yaml:"apiVersion"`
}

// Configuration for the git-controller.
type Config struct {
	// Rules to load.
	Rules []Rule `yaml:"rules"`
	// Kinds for the controller to watch.
	Kinds []Kind `yaml:"kinds"`
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

func (c *Config) RuleForObject(k8sState runtime.Object, gitState runtime.Object) (*Rule, error) {
	for _, rule := range c.Rules {
		match, err := rule.Matches(k8sState, gitState)
		if err != nil {
			return nil, err
		}

		if match {
			return &rule, nil
		}
	}

	return nil, nil
}

type Source struct {
	Kind runtime.Object
	Chan chan event.GenericEvent
}

// Reconciler that synchronizes objects in Kubernetes to a git repository.
type Reconciler struct {
	config  *Config
	client  client.Client
	repo    *repo.Repo
	mgr     manager.Manager
	restMap meta.RESTMapper
	repoDir string
	sources []Source
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

	r := &Reconciler{
		config: config,
		repo:   repo,
		mgr:    mgr,
		restMap: restMap,
		client: mgr.GetClient(),
		sources: []Source{},
	}

	for _, kinds := range config.Kinds {
		err = r.Register(util.Kind(kinds.Kind, kinds.Group, kinds.APIVersion))
		if err != nil {
			return nil, err
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

		// Synchronize to Git or Kubernetes, depending on the SyncTo type of the rule.
		util.Log.Info("syncing", "kind", strKind, "name", name,
		              "namespace", namespace, "syncTo", rule.SyncTo)

		if rule.SyncTo == Git {
			err = r.SyncObjectToGit(k8sState, gitState)
		} else {
			err = r.SyncObjectToKubernetes(k8sState, gitState)
		}

		return reconcile.Result{}, err
	})
}

func (r *Reconciler) SyncObjectToGit(k8sState runtime.Object, gitState *ryaml.Object) error {
	var err error

	if k8sState == nil {
		err = r.repo.RemoveResource(k8sState, gitState)
	} else {
		err = r.repo.AddResource(k8sState, gitState)
	}

	if err != nil {
		return err
	}

	return r.repo.Push()
}

func (r *Reconciler) SyncObjectToKubernetes(k8sState runtime.Object, gitState *ryaml.Object) error {
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
		if err := r.client.Delete(context.TODO(), k8sState); err != nil && ! errors.IsNotFound(err) {
			return err
		}
	  return nil
	}

	if k8sState == nil {
		util.Log.Info("recreating object from git", "kind", kind, "name",
					  logMeta.GetName(), "namespace", logMeta.GetNamespace())
		return r.client.Create(context.TODO(), gitState.Object)
	}

	k8sMeta := util.GetMeta(k8sState)
	gitMeta := util.GetMeta(gitState.Object)
	gitMeta.SetResourceVersion(k8sMeta.GetResourceVersion())

	util.Log.Info("restoring object to git state", "kind", kind, "name",
					  logMeta.GetName(), "namespace", logMeta.GetNamespace())

	return r.client.Update(context.TODO(), gitState.Object)
}

func (r *Reconciler) RegisterReconcilerForType(kind runtime.Object) error {
	strKind := kind.GetObjectKind().GroupVersionKind().Kind
	name := fmt.Sprintf("%s-controller", strKind)
	util.Log.Info("starting controller", "kind", strKind)

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
				Meta: meta,
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
