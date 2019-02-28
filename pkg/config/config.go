package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/appscode/jsonpatch"
	patchapply "github.com/evanphx/json-patch"
	"github.com/jinzhu/inflection"
	"github.com/justinbarrick/gitops-controller/pkg/util"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"strings"
)

type SyncType string

const (
	Kubernetes SyncType = "kubernetes"
	Git        SyncType = "git"
)

func PatchObject(original, current runtime.Object, rule *Rule) (runtime.Object, error) {
	patch := admission.PatchResponse(original, current)
	patches := []jsonpatch.Operation{}

	for _, patch := range patch.Patches {
		matched := false

		if len(rule.Filters) == 0 {
			matched = true
		}

		for _, filter := range rule.Filters {
			match, err := util.PatchMatchesPath(patch, filter)
			if err != nil {
				return nil, err
			}

			if !match {
				continue
			}

			matched = true
		}

		if !matched {
			continue
		}

		patches = append(patches, patch)
	}

	serialized, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}

	serializedPatches, err := json.Marshal(patches)
	if err != nil {
		return nil, err
	}

	patchOps, err := patchapply.DecodePatch(serializedPatches)
	if err != nil {
		return nil, err
	}

	serialized, err = patchOps.Apply(serialized)
	if err != nil {
		return nil, err
	}

	final := &unstructured.Unstructured{}
	if err := kyaml.NewYAMLOrJSONDecoder(bytes.NewBuffer(serialized), len(serialized)).Decode(final); err != nil {
		return nil, err
	}

	k8sMeta := util.GetMeta(original)
	finalMeta := util.GetMeta(final)
	finalMeta.SetResourceVersion(k8sMeta.GetResourceVersion())
	return final, nil
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
	// A list of JSON path expressions that changes will be restricted to (e.g., `/metadata/annotations` will ignore
	// any changes that are not to annotations).
	Filters []string `yaml:"filters"`
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
func (r *Rule) Matches(k8sState runtime.Object, gitState runtime.Object, typeOnly bool) (bool, error) {
	var obj runtime.Object
	if k8sState != nil {
		obj = k8sState
	} else {
		obj = gitState
	}

	kind := util.GetType(obj)

	if !util.Contains(r.NormalizedResources(), strings.ToLower(kind.Kind)) {
		return false, nil
	}

	if !util.Contains(r.APIGroups, kind.Group) {
		return false, nil
	}

	if typeOnly {
		return true, nil
	}

	original := gitState
	current := k8sState
	if r.SyncTo == Kubernetes {
		original = gitState
		current = k8sState
	}

	if original != nil && current != nil {
		patch := admission.PatchResponse(original, current)
		matches := len(r.Filters) == 0
		for _, filter := range r.Filters {
			for _, patch := range patch.Patches {
				match, err := util.PatchMatchesPath(patch, filter)
				if err != nil {
					return false, err
				}
				if match {
					matches = match
					break
				}
			}
		}

		if !matches {
			return false, nil
		}
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

		if !labelSelector.Matches(labels.Set(objLabels)) {
			return false, nil
		}
	}

	return true, nil
}

// Configuration for the gitops-controller.
type Config struct {
	// Path inside of the Git repository to use as working directory.
	GitPath string `yaml:"gitPath,omitempty"`
	// URL to the Git repository to clone.
	GitURL string `yaml:"gitUrl,omitempty"`
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

	config.GitPath = *flag.String("git-path", config.GitPath, "The path inside of the Git repository to work in.")
	config.GitURL = *flag.String("git-url", config.GitURL, "The URL to the Git repository to clone")

	flag.Parse()

	if config.GitURL == "" {
		return nil, fmt.Errorf("No -git-url provided.")
	}

	return config, nil
}

func (c *Config) RuleForObject(k8sState runtime.Object, gitState runtime.Object, typeOnly bool) (*Rule, error) {
	for _, rule := range c.Rules {
		match, err := rule.Matches(k8sState, gitState, typeOnly)
		if err != nil {
			return nil, err
		}

		if match {
			return &rule, nil
		}
	}

	return nil, nil
}
