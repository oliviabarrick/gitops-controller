package reconciler

import (
	"gopkg.in/yaml.v2"
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/justinbarrick/git-controller/pkg/util"
)

func TestRule(t *testing.T) {
	config := `
rules:
- apiGroups:
  - snapshot.storage.k8s.io
  resources: 
  - volumesnapshots
  - volumesnapshotcontents
  labels: sync=true
  filters:
  - .metadata.annotations
  sync: kubernetes
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
	assert.Equal(t, []string{".metadata.annotations"}, rule.Filters)
	assert.Equal(t, Kubernetes, rule.Sync)
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
