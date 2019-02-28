package config

import (
	"github.com/justinbarrick/gitops-controller/pkg/util"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/runtime"
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
		name     string
		matches  bool
		typeOnly bool
		k8sObj   runtime.Object
		gitObj   runtime.Object
		rule     Rule
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
		{
			name:    "type only ignores filters and labels",
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: true,
			typeOnly: true,
			rule: Rule{
				APIGroups: []string{"extensions"},
				Resources: []string{"deployments"},
				Filters: []string{
					"/nonexistant",
				},
				Labels: "wrong=label",
				SyncTo: Kubernetes,
			},
		},
		{
			name:    "type only ignores filters and labels (mismatch)",
			k8sObj:  util.DefaultObject(deployment, "test", "hello"),
			matches: false,
			typeOnly: true,
			rule: Rule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Filters: []string{
					"/nonexistant",
				},
				Labels: "wrong=label",
				SyncTo: Kubernetes,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			matches, err := test.rule.Matches(test.k8sObj, test.gitObj, test.typeOnly)
			assert.Nil(t, err)
			assert.Equal(t, test.matches, matches)
		})
	}
}
