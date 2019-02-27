package reconciler

import (
	"context"
	"github.com/justinbarrick/gitops-controller/pkg/repo"
	"github.com/justinbarrick/gitops-controller/pkg/util"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
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

func TestRuleLoadYAML(t *testing.T) {
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

func TestRules(t *testing.T) {
	deployment := util.Kind("Deployment", "extensions", "v1beta1")

	for _, test := range []struct {
		name    string
		matches bool
		k8sObj  runtime.Object
		gitObj  runtime.Object
		rule    Rule
	}{
		{
			name:    "api group matching",
			matches: true,
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			rule: Rule{
				APIGroups: []string{"extensions"},
				Resources: []string{"deployments"},
				SyncTo:    Kubernetes,
			},
		},
		{
			name:    "api group mis-match",
			matches: false,
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			rule: Rule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				SyncTo:    Kubernetes,
			},
		},
		{
			name:    "resources match",
			matches: true,
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			rule: Rule{
				Resources: []string{"deployments"},
				SyncTo:    Kubernetes,
			},
		},
		{
			name:    "resources mis-match",
			matches: false,
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			rule: Rule{
				APIGroups: []string{"blah"},
				SyncTo:    Kubernetes,
			},
		},
		{
			name:    "empty resource, matches any resource",
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: true,
			rule: Rule{
				APIGroups: []string{"extensions"},
				SyncTo:    Kubernetes,
			},
		},
		{
			name:    "labels match",
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			gitObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			matches: true,
			rule: Rule{
				Labels: "a=label",
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "labels do not match",
			k8sObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			gitObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: false,
			rule: Rule{
				Labels: "a=label",
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "labels not set",
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			gitObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: false,
			rule: Rule{
				Labels: "a=label",
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "resource not in git",
			k8sObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			matches: false,
			rule: Rule{
				Labels: "a=label",
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "missing from kubernetes matches git labels",
			gitObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			matches: true,
			rule: Rule{
				Labels: "a=label",
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "filter rules match change details",
			gitObj:  annotated(util.DefaultObject(deployment, "test", "hello")),
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: true,
			rule: Rule{
				Filters: []string{
					"/metadata/annotations",
				},
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "filter rules mis-match change details",
			gitObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: false,
			rule: Rule{
				Filters: []string{
					"/metadata/annotations",
				},
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "filter rules match changes underneath filter",
			gitObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: true,
			rule: Rule{
				Filters: []string{
					"/metadata",
				},
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "filter rules mis-match changes not underneath filter",
			gitObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: false,
			rule: Rule{
				Filters: []string{
					"/spec",
				},
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "filter always matches if the object needs to be created or deleted",
			gitObj:  labeled(util.DefaultObject(deployment, "test", "hello")),
			matches: true,
			rule: Rule{
				Filters: []string{
					"/spec",
				},
				SyncTo: Kubernetes,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			matches, err := test.rule.Matches(test.k8sObj, test.gitObj)
			assert.Nil(t, err)
			assert.Equal(t, test.matches, matches)
		})
	}
}

func TestReconciler(t *testing.T) {
	deployment := util.Kind("Deployment", "extensions", "v1beta1")

	for _, test := range []struct {
		name        string
		kind        runtime.Object
		testObj     types.NamespacedName
		initK8s     runtime.Object
		initGit     runtime.Object
		expectedGit runtime.Object
		expectedK8s runtime.Object
		rules       []Rule
	}{
		{
			name:        "Git rule adds objects in kubernetes to kubernetes",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initK8s:     util.DefaultObject(deployment, "test", "hello"),
			expectedGit: util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Git,
				},
			},
		},
		{
			name:        "Kubernetes rule adds objects in git to kubernetes",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initGit:     util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedGit: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Kubernetes,
				},
			},
		},
		{
			name:    "Git rule deletes objects missing from kubernetes",
			kind:    deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initGit: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Git,
				},
			},
		},
		{
			name:    "Kubernetes rule deletes objects missing from git",
			kind:    deployment,
			testObj: types.NamespacedName{"hello", "test"},
			initK8s: util.DefaultObject(deployment, "test", "hello"),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Kubernetes,
				},
			},
		},
		{
			name:        "Git rule updates out of date objects from kubernetes",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initGit:     util.DefaultObject(deployment, "test", "hello"),
			initK8s:     annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedK8s: annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Git,
				},
			},
		},
		{
			name:        "Kubernetes rule updates out of date objects from git repository",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initGit:     annotated(util.DefaultObject(deployment, "test", "hello")),
			initK8s:     util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Kubernetes,
				},
			},
		},
		{
			name:        "First rule is applied",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initGit:     annotated(util.DefaultObject(deployment, "test", "hello")),
			initK8s:     util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Kubernetes,
				},
				Rule{
					Resources: []string{"deployments"},
					APIGroups: []string{"extensions"},
					SyncTo:    Git,
				},
			},
		},
		{
			name:        "No match does not sync",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initGit:     annotated(util.DefaultObject(deployment, "test", "hello")),
			initK8s:     util.DefaultObject(deployment, "test", "hello"),
			expectedK8s: util.DefaultObject(deployment, "test", "hello"),
			expectedGit: annotated(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Resources: []string{"secrets"},
					APIGroups: []string{""},
					SyncTo:    Git,
				},
			},
		},
		{
			name:        "Resource label matches",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initGit:     labeled(util.DefaultObject(deployment, "test", "hello")),
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
			name:    "Delete does not crash",
			kind:    deployment,
			testObj: types.NamespacedName{"hello", "test"},
			rules:   []Rule{},
		},
		{
			name:        "Rule with filters respects filters when patching",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initK8s:     annotated(util.DefaultObject(deployment, "test", "hello")),
			initGit:     labeled(util.DefaultObject(deployment, "test", "hello")),
			expectedK8s: annotated(labeled(util.DefaultObject(deployment, "test", "hello"))),
			expectedGit: labeled(util.DefaultObject(deployment, "test", "hello")),
			rules: []Rule{
				Rule{
					Filters: []string{"/metadata/labels"},
					SyncTo:  Kubernetes,
				},
			},
		},
		{
			name:        "Git rule with filters respects filters when patching",
			kind:        deployment,
			testObj:     types.NamespacedName{"hello", "test"},
			initK8s:     labeled(util.DefaultObject(deployment, "test", "hello")),
			initGit:     annotated(util.DefaultObject(deployment, "test", "hello")),
			expectedK8s: labeled(util.DefaultObject(deployment, "test", "hello")),
			expectedGit: annotated(labeled(util.DefaultObject(deployment, "test", "hello"))),
			rules: []Rule{
				Rule{
					Filters: []string{"/metadata/labels"},
					SyncTo:  Git,
				},
			},
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
				repo:   repo,
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
