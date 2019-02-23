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
	"github.com/justinbarrick/backup-controller/pkg/runtime"
	"log"
	"context"
	"k8s.io/apimachinery/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	runtimeObj "k8s.io/apimachinery/pkg/runtime"
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
	scheme = runtimeObj.NewScheme()
	defaulter = runtimeObj.ObjectDefaulter(scheme)
)

const (
	webhookNamespace = "git-controller"
	webhookName      = "git-controller"
)

// Reconciler for reacting to PersistentVolumeClaim events.
type Reconciler struct {
	lock sync.Mutex
	client  client.Client
	runtime *runtime.Runtime
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

// Returns the type that the reconciler watches.
func (r *Reconciler) GetType() []runtimeObj.Object {
	return []runtimeObj.Object{
		&snapshots.VolumeSnapshot{},
	}
}

// Set the Kubernetes client.
func (r *Reconciler) SetClient(client client.Client) {
	r.client = client
}

// Set the backup-controller runtime.
func (r *Reconciler) SetRuntime(runtime *runtime.Runtime) {
	r.runtime = runtime
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

func (r *Reconciler) GitPathForResource(kind, namespace, name string) string {
	return filepath.Join(kind, namespace, name + ".yaml")
}

func (r *Reconciler) PathForResource(resource runtimeObj.Object) string {
	strKind := resource.GetObjectKind().GroupVersionKind().Kind
	meta, _ := meta.Accessor(resource)
	return filepath.Join(r.repoDir, r.GitPathForResource(strKind, meta.GetNamespace(), meta.GetName()))
}

func (r *Reconciler) SerializeResource(resource runtimeObj.Object) error {
	meta, err := meta.Accessor(resource)
	if err != nil {
		return err
	}

	encoder := json.NewYAMLSerializer(json.DefaultMetaFactory, nil, nil)
	fullPath := r.PathForResource(resource)

	err = os.MkdirAll(filepath.Dir(fullPath), 0700)
	if err != nil {
		return err
	}

	outFile, err := os.Create(fullPath)
	if err != nil {
		return err
	}

	defer outFile.Close()

	meta.SetResourceVersion("")
	meta.SetCreationTimestamp(metav1.Time{})
	meta.SetSelfLink("")
	meta.SetUID(types.UID(""))
	meta.SetGeneration(0)

	return encoder.Encode(resource, outFile)
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
func (r *Reconciler) ReconcilerForType(mgr manager.Manager, kind runtimeObj.Object) error {
	strKind := kind.GetObjectKind().GroupVersionKind().Kind
	log.Printf("Starting controller for %s", strKind)

	ctrlr, err := controller.New(fmt.Sprintf("%s-controller", strKind), mgr, controller.Options{
		Reconciler: reconcile.Func(func(request reconcile.Request) (reconcile.Result, error) {
			r.lock.Lock()
			defer r.lock.Unlock()

			log.Println("Reconciling:", request.NamespacedName)

			obj := kind.DeepCopyObject()
			gitPath := r.GitPathForResource(strKind, request.NamespacedName.Namespace, request.NamespacedName.Name)

			err := r.client.Get(context.TODO(), request.NamespacedName, obj)
			if err != nil {
				if errors.IsNotFound(err) {
					log.Println("Snapshot not found:", request.NamespacedName)

					if err := r.Remove(gitPath); err != nil {
						return reconcile.Result{}, err
					}

					if err := r.Commit(fmt.Sprintf("Deleting %s", request.NamespacedName.String())); err != nil {
						return reconcile.Result{}, err
					}

					return reconcile.Result{}, nil
				}

				return reconcile.Result{}, err
			}

			if err := r.SerializeResource(obj); err != nil {
				return reconcile.Result{}, err
			}

			if err := r.Add(gitPath); err != nil {
				return reconcile.Result{}, err
			}

			return reconcile.Result{}, r.Commit(fmt.Sprintf("Updated %s", gitPath))
		}),
	})
	if err != nil {
		return err
	}

	return ctrlr.Watch(&source.Kind{
		Type: kind,
	}, &handler.EnqueueRequestForObject{})
}

func init() {
	snapshots.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	extensions.AddToScheme(scheme)
}

func main() {
	decode := serializer.NewCodecFactory(scheme).UniversalDeserializer().Decode

	err := filepath.Walk("/tmp/myrepo", func(path string, info os.FileInfo, err error) error {
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

		opened, err := os.Open(path)
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

			meta, err := meta.Accessor(obj)
			if err != nil {
				return err
			}

			fmt.Println(path, meta.GetNamespace(), meta.GetName(), obj.GetObjectKind().GroupVersionKind().Kind)
		}

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

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
