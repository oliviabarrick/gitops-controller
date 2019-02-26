package reconciler

import (
	"context"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"gopkg.in/yaml.v2"
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/justinbarrick/git-controller/pkg/util"
	"github.com/justinbarrick/git-controller/pkg/repo"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"k8s.io/apimachinery/pkg/runtime"
)

func annotated(obj runtime.Object) runtime.Object {
	meta := util.GetMeta(obj)
	meta.SetAnnotations(map[string]string{
		"an": "annotation",
	})
	return obj
}

func labeled(obj runtime.Object) runtime.Object {
	meta := util.GetMeta(obj)
	meta.SetLabels(map[string]string{
		"a": "label",
	})
	return obj
}

func TestRule(t *testing.T) {
	config := `
rules:
- apiGroups:
  - snapshot.storage.k8s.io
  resources: 
  - volumesnapshots
  - volumesnapshotcontents
  labels: sync=true
  syncTo: kubernetes
`
	loaded := &Config{}

	err := yaml.Unmarshal([]byte(config), loaded)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(loaded.Rules))

	rule := loaded.Rules[0]
	assert.Equal(t, []string{"snapshot.storage.k8s.io"}, rule.APIGroups)
	assert.Equal(t, []string{
		"volumesnapshots", "volumesnapshotcontents",
	}, rule.Resources)
	assert.Equal(t, "sync=true", rule.Labels)
	assert.Equal(t, Kubernetes, rule.SyncTo)
}

func TestRuleMatch(t *testing.T) {
	snap := util.Kind("Deployment", "extensions", "v1beta1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")

	rule := &Rule{
		APIGroups: []string{"extensions"},
		Resources: []string{"deployments"},
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, true, matches)
}

func TestRuleNoMatch(t *testing.T) {
	snap := util.Kind("Deployment", "extensions", "v1beta1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")

	rule := &Rule{
		APIGroups: []string{"snapshot.storage.k8s.io"},
		Resources: []string{"volumesnapshots", "volumesnapshotcontents"},
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, false, matches)
}

func TestRuleMatchNoGroup(t *testing.T) {
	snap := util.Kind("Deployment", "extensions", "v1beta1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")

	rule := &Rule{
		Resources: []string{"deployments"},
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, true, matches)
}

func TestRuleNoMatchNoGroup(t *testing.T) {
	snap := util.Kind("Secret", "", "v1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")

	rule := &Rule{
		Resources: []string{"deployments"},
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, false, matches)
}

func TestRuleMatchNoResources(t *testing.T) {
	snap := util.Kind("Deployment", "extensions", "v1beta1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")

	rule := &Rule{
		APIGroups: []string{"extensions"},
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, true, matches)
}

func TestRuleNoMatchNoResources(t *testing.T) {
	snap := util.Kind("Deployment", "extensions", "v1beta1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")

	rule := &Rule{
		APIGroups: []string{""},
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, false, matches)
}

func TestRuleMatchLabels(t *testing.T) {
	snap := util.Kind("Deployment", "extensions", "v1beta1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")
	meta.SetLabels(map[string]string{"hello": "world",})

	rule := &Rule{
		Labels: "hello=world",
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, true, matches)
}

func TestRuleNoMatchLabels(t *testing.T) {
	snap := util.Kind("Deployment", "extensions", "v1beta1")
	meta := util.GetMeta(snap)
	meta.SetName("my-snapshot")
	meta.SetNamespace("my-namespace")

	rule := &Rule{
		Labels: "hello=world",
	}

	matches, err := rule.Matches(snap)
	assert.Nil(t, err)
	assert.Equal(t, false, matches)
}

func TestReconciler(t *testing.T) {
	deployment := util.Kind("Deployment", "extensions", "v1beta1")

	for _, test := range []struct {
		name string
		kind runtime.Object
		testObj types.NamespacedName
		initK8s runtime.Object
		initGit runtime.Object
		expectedGit runtime.Object
		expectedK8s runtime.Object
		rules []Rule
	} {
		{
			name: "Git rule adds objects in kubernetes to kubernetes",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedGit: util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Git,
				},
			},
		},
		{
			name: "Kubernetes rule adds objects in git to kubernetes",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedGit: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Kubernetes,
				},
			},
		},
		{
			name: "Git rule deletes objects missing from kubernetes",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Git,
				},
			},
		},
		{
			name: "Kubernetes rule deletes objects missing from git",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initK8s: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Kubernetes,
				},
			},
		},
		{
			name: "Git rule updates out of date objects from kubernetes",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: util.DefaultObject(deployment, "test", "hello"),
			initK8s: annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedK8s: annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Git,
				},
			},
		},
		{
			name: "Kubernetes rule updates out of date objects from git repository",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			initK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Kubernetes,
				},
			},
		},
		{
			name: "First rule is applied",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			initK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Kubernetes,
				},
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo: Git,
				},
			},
		},
		{
			name: "No match does not sync",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			initK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"secrets"},
					APIGroups: []string{""},
					SyncTo: Git,
				},
			},
		},
		{
			name: "Resource label matches",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: labeled(util.DefaultObject(deployment, "test", "hello")),
			expectedK8s: labeled(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: labeled(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Labels: "a=label",
					SyncTo: Kubernetes,
				},
			},
		},
		{
			name: "Delete does not crash",
			kind: deployment,
			testObj: types.NamespacedName{"hello", "test"},
			rules: []Rule{},
		},

	} {
		t.Run(test.name, func(t *testing.T) {
			// Initialize empty repo for test.
			repo, err := repo.NewRepo("", ".")
			assert.Nil(t, err)

			// If initGit is not nil, add initGit to the repo.
			if test.initGit != nil {
				err = repo.AddResource(test.initGit, nil)
				assert.Nil(t, err)
			}

			// Initialize the Kubernetes client with the object in initk8s (if
			// it is nil).
			initObjs := []runtime.Object{}
			if test.initK8s != nil {
				initObjs = append(initObjs, test.initK8s)
			}
			client := fake.NewFakeClient(initObjs...)

			reconciler := &Reconciler{
				client: client,
				repo: repo,
				config: &Config{
					Rules: test.rules,
				},
			}

			// Run the reconciler method.
			_, err = reconciler.ReconcilerForType(test.kind)(reconcile.Request{
				test.testObj,
			})
			assert.Nil(t, err)

			actual := util.DefaultObject(test.kind, test.testObj.Name, test.testObj.Namespace)

			// Verify that the object is set to expectedGit. If expectedGit is nil,
			// then testObj should be missing.
			obj, err := repo.FindObjectInRepo(actual)
			assert.Nil(t, err)
			if test.expectedGit == nil {
				assert.Nil(t, obj)
			} else {
				assert.NotNil(t, obj)
				if obj == nil {
					return
				}
				assert.Equal(t, test.expectedGit, obj.Object)
			}

			// Verify that the object in Kubernetes is set to expectedK8s. If it is nil,
			// then testObj should be missing.
			err = client.Get(context.TODO(), test.testObj, actual)
			if test.expectedK8s == nil {
				assert.NotNil(t, err)
				assert.Equal(t, true, errors.IsNotFound(err))
			} else {
				assert.Nil(t, err)
				assert.Equal(t, test.expectedK8s, actual)
			}
		})
	}
}
