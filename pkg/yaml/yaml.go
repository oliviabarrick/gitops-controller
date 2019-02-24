package yaml

import (
	"bufio"
	"github.com/justinbarrick/git-controller/pkg/util"
	"io"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	rSchema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"log"
	"os"
	"path/filepath"
)

type ObjectMapping struct {
	File   *YAMLFile
	Object runtime.Object
}

func GetMeta(obj runtime.Object) metav1.Object {
	meta, _ := meta.Accessor(obj)
	return meta
}

func GetType(obj runtime.Object) rSchema.GroupVersionKind {
	return obj.GetObjectKind().GroupVersionKind()
}

func (o *ObjectMapping) Matches(obj runtime.Object) bool {
	actualMeta := GetMeta(o.Object)
	expectedMeta := GetMeta(obj)
	actualType := GetType(o.Object)
	expectedType := GetType(obj)

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
	return GetMeta(o.Object).GetName()
}

func (o *ObjectMapping) Namespace() string {
	return GetMeta(o.Object).GetNamespace()
}

func (o *ObjectMapping) Kind() string {
	return GetType(o.Object).Kind
}

func (o *ObjectMapping) Save() error {
	if o.File == nil {
		yamlFile := NewYAMLFile("/tmp/myrepo/volumesnapshot.yaml")
		yamlFile.AddResource(o)
	}

	log.Println("Saving object: ", o.File.Path)
	return o.File.Dump()
}

type YAMLFile struct {
	Objects []*ObjectMapping
	Path    string
}

func NewYAMLFile(path string) *YAMLFile {
	return &YAMLFile{
		Path:    path,
		Objects: []*ObjectMapping{},
	}
}

func (y *YAMLFile) GetResource(resource runtime.Object) *ObjectMapping {
	for _, obj := range y.Objects {
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
	y.Objects = append(y.Objects, obj)
	obj.File = y
}

func (y *YAMLFile) Load() error {
	decode := serializer.NewCodecFactory(util.Scheme).UniversalDeserializer().Decode

	opened, err := os.Open(y.Path)
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

		y.Objects = append(y.Objects, &ObjectMapping{
			File:   y,
			Object: obj,
		})
	}

	return nil
}

func (y *YAMLFile) Dump() error {
	encoder := json.NewYAMLSerializer(json.DefaultMetaFactory, nil, nil)

	log.Println("Dumping file: ", y.Path)
	if err := os.MkdirAll(filepath.Dir(y.Path), 0700); err != nil {
		return err
	}

	outFile, err := os.Create(y.Path)
	if err != nil {
		return err
	}

	defer outFile.Close()

	for index, obj := range y.Objects {
		if index != 0 {
			outFile.Write([]byte("---\n"))
		}

		meta := GetMeta(obj.Object)
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
