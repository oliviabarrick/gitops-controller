package main

import (
	"io"
	"bufio"
	"k8s.io/apimachinery/pkg/util/yaml"
	"strings"
	extensions "k8s.io/api/extensions/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"fmt"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"path/filepath"
	"gopkg.in/src-d/go-git.v4"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"os"
	snapshots "github.com/kubernetes-csi/external-snapshotter/pkg/apis/volumesnapshot/v1alpha1"
	"log"
	"context"
	"k8s.io/apimachinery/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	rSchema "k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sync"
)

var (
	scheme = runtime.NewScheme()
	defaulter = runtime.ObjectDefaulter(scheme)
)

const (
	webhookNamespace = "git-controller"
	webhookName      = "git-controller"
)

type ObjectMapping struct {
	File *YAMLFile
	Object runtime.Object
}

func getMeta(obj runtime.Object) metav1.Object {
	meta, _ := meta.Accessor(obj)
	return meta
}

func getType(obj runtime.Object) rSchema.GroupVersionKind {
	return obj.GetObjectKind().GroupVersionKind()
}

func (o *ObjectMapping) Matches(obj runtime.Object) bool {
	actualMeta := getMeta(o.Object)
	expectedMeta := getMeta(obj)
	actualType := getType(o.Object)
	expectedType := getType(obj)

	if actualMeta.GetName() != expectedMeta.GetName() {
		return false
	}

	if actualMeta.GetNamespace() != expectedMeta.GetNamespace() {
		return false
	}

	if actualType.Kind != expectedType.Kind {
		return false
	}

	return true
}

func (o *ObjectMapping) Name() string {
	return getMeta(o.Object).GetName()
}

func (o *ObjectMapping) Namespace() string {
	return getMeta(o.Object).GetNamespace()
}

func (o *ObjectMapping) Kind() string {
	return getType(o.Object).Kind
}

func (o *ObjectMapping) Save() error {
	if o.File == nil {
		yamlFile := NewYAMLFile("/tmp/myrepo/volumesnapshot.yaml")
		yamlFile.AddResource(o)
	}

	log.Println("Saving object: ", o.File.path)
	return o.File.Dump()
}

type YAMLFile struct {
	objects []*ObjectMapping
	path string
}

func NewYAMLFile(path string) *YAMLFile {
	return &YAMLFile{
		path: path,
		objects: []*ObjectMapping{},
	}
}

func (y *YAMLFile) GetResource(resource runtime.Object) *ObjectMapping {
	for _, obj := range y.objects {
		if obj.Matches(resource) {
			return obj
		}
	}

	return nil
}

func (y *YAMLFile) SetResource(resource runtime.Object) {
	obj := y.GetResource(resource)
	obj.Object = resource
}

func (y *YAMLFile) AddResource(obj *ObjectMapping) {
	y.objects = append(y.objects, obj)
	obj.File = y
}

func (y *YAMLFile) Load() error {
	decode := serializer.NewCodecFactory(scheme).UniversalDeserializer().Decode

	opened, err := os.Open(y.path)
	if err != nil {
		return err
	}
	defer opened.Close()

	yamlReader := yaml.NewYAMLReader(bufio.NewReader(opened))

	for {
		data, err := yamlReader.Read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		obj, _, err := decode(data, nil, nil)
		if err != nil {
			return err
		}

		y.objects = append(y.objects, &ObjectMapping{
			File: y,
			Object: obj,
		})
	}

	return nil
}

func (y *YAMLFile) Dump() error {
	encoder := json.NewYAMLSerializer(json.DefaultMetaFactory, nil, nil)

	log.Println("Dumping file: ", y.path)
	if err := os.MkdirAll(filepath.Dir(y.path), 0700); err != nil {
		return err
	}

	outFile, err := os.Create(y.path)
	if err != nil {
		return err
	}

	defer outFile.Close()

	for index, obj := range y.objects {
		if index != 0 {
			outFile.Write([]byte("---\n"))
		}

		meta := getMeta(obj.Object)
		log.Println("Dumping object: ", meta.GetName())

		meta.SetResourceVersion("")
		meta.SetCreationTimestamp(metav1.Time{})
		meta.SetSelfLink("")
		meta.SetUID(types.UID(""))
		meta.SetGeneration(0)

		err = encoder.Encode(obj.Object, outFile)
		if err != nil {
			return err
		}
	}

	return nil
}

type Reconciler struct {
	lock sync.Mutex
	client  client.Client
	repo *git.Repository
	tree *git.Worktree
	repoDir string
}

func NewReconciler(repoDir string) (*Reconciler, error) {
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		return nil, err
	}

	tree, err := repo.Worktree()
	if err != nil {
		return nil, err
	}

	return &Reconciler{
		repo: repo,
		tree: tree,
		repoDir: repoDir,
	}, nil
}

// Set the Kubernetes client.
func (r *Reconciler) SetClient(client client.Client) {
	r.client = client
}

func (r *Reconciler) IsClean() (bool, error) {
	status, err := r.tree.Status()
	if err != nil {
		return false, err
	}

	return status.IsClean(), nil
}

func (r *Reconciler) Commit(message string) error {
	clean, err := r.IsClean()
	if err != nil {
		return err
	}

	// nothing to do
	if clean {
		return nil
	}

	_, err = r.tree.Commit(message, &git.CommitOptions{
		Author: &object.Signature{
			Name: "test",
			Email: "test@test.com",
		},
	})
	return err
}

func (r *Reconciler) Remove(path string) error {
	_, err := r.tree.Remove(path)
	return err
}

func (r *Reconciler) Add(path string) error {
	_, err := r.tree.Add(path)
	return err
}

// Reconcile PersistentVolumeClaims by updating the backups map with information about the PVC.
func (r *Reconciler) ReconcilerForType(mgr manager.Manager, kind runtime.Object) error {
	strKind := kind.GetObjectKind().GroupVersionKind().Kind
	log.Printf("Starting controller for %s", strKind)

	ctrlr, err := controller.New(fmt.Sprintf("%s-controller", strKind), mgr, controller.Options{
		Reconciler: reconcile.Func(func(request reconcile.Request) (reconcile.Result, error) {
			r.lock.Lock()
			defer r.lock.Unlock()

			log.Println("Reconciling:", request.NamespacedName)

			obj := kind.DeepCopyObject()
			getMeta(obj).SetName(request.NamespacedName.Name)
			getMeta(obj).SetNamespace(request.NamespacedName.Namespace)

			err := r.client.Get(context.TODO(), request.NamespacedName, obj)
			if err != nil && ! errors.IsNotFound(err) {
				return reconcile.Result{}, err
			}

			resource, err := r.FindObjectInRepo(obj)
			if err != nil {
				return reconcile.Result{}, err
			}

			if resource == nil {
				resource = &ObjectMapping{}
			}

			resource.Object = obj

			if err := resource.Save(); err != nil {
				return reconcile.Result{}, err
			}

			gitPath, err := filepath.Rel("/tmp/myrepo/", resource.File.path)
			if err != nil {
				return reconcile.Result{}, err
			}

			if err = r.Add(gitPath); err != nil {
				return reconcile.Result{}, err
			}

			commit := fmt.Sprintf("Updating %s", request.NamespacedName.String())
			return reconcile.Result{}, r.Commit(commit)
		}),
	})
	if err != nil {
		return err
	}

	return ctrlr.Watch(&source.Kind{
		Type: kind,
	}, &handler.EnqueueRequestForObject{})
}

func (r *Reconciler) LoadReposYAMLs() ([]*ObjectMapping, error) {
	mappings := []*ObjectMapping{}

	return mappings, filepath.Walk("/tmp/myrepo", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel("/tmp/myrepo/.git", path)
		if err != nil {
			return nil
		}

		if ! strings.HasPrefix(rel, "../") {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		file := NewYAMLFile(path)

		err = file.Load()
		if err != nil {
			return err
		}

		mappings = append(mappings, file.objects...)
		return nil
	})
}

func (r *Reconciler) FindObjectInRepo(obj runtime.Object) (*ObjectMapping, error) {
	var found *ObjectMapping

	objectMappings, err := r.LoadReposYAMLs()
	if err != nil {
		return found, err
	}

	for _, objMapping := range objectMappings {
		if ! objMapping.Matches(obj) {
			continue
		}

		found = objMapping
		break
	}

	return found, nil
}

func init() {
	snapshots.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	extensions.AddToScheme(scheme)
}

func main() {
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		Scheme: scheme,
	})
	if err != nil {
		log.Fatal(err)
	}

	reconciler, err := NewReconciler("/tmp/myrepo")
	if err != nil {
		log.Fatal("cannot open repository:", err)
	}

	reconciler.SetClient(mgr.GetClient())

	if err := reconciler.ReconcilerForType(mgr, &snapshots.VolumeSnapshot{
		TypeMeta: metav1.TypeMeta{ Kind: "VolumeSnapshot", },
	}); err != nil {
		log.Fatal("cannot initialize volumesnapshot reconciler:", err)
	}

	if err := reconciler.ReconcilerForType(mgr, &snapshots.VolumeSnapshotContent{
		TypeMeta: metav1.TypeMeta{ Kind: "VolumeSnapshotContent", },
	}); err != nil {
		log.Fatal("cannot initialize volumesnapshotcontent reconciler:", err)
	}

/*
	if err := reconciler.ReconcilerForType(mgr, &extensions.Deployment{
		TypeMeta: metav1.TypeMeta{ Kind: "Deployment", },
	}); err != nil {
		log.Fatal("cannot initialize volumesnapshotcontent reconciler:", err)
	}
*/

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Fatal("cannot start manager:", err)
	}
}
