package yaml

import (
	"bufio"
	"github.com/justinbarrick/git-controller/pkg/util"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"log"
	"os"
	"path/filepath"
)

// Allows manipulation of objects in a YAML file without disturbing the other
// objects.
type File struct {
	Objects []*Object
	Path    string
}

// Instantiate a new YAML file.
func NewFile(path string) *File {
	return &File{
		Path:    path,
		Objects: []*Object{},
	}
}

// Add a resource to the file.
func (y *File) AddResource(obj *Object) {
	for _, object := range y.Objects {
		if object.Matches(obj.Object) {
			return
		}
	}

	y.Objects = append(y.Objects, obj)
	obj.File = y
}

// Remove a resource from the file.
func (y *File) RemoveResource(obj *Object) {
	objects := []*Object{}

	for _, object := range y.Objects {
		if object.Matches(obj.Object) {
			log.Println("Pruning resource from file", util.GetMeta(obj.Object).GetName())
			continue
		}

		objects = append(objects, object)
	}

	y.Objects = objects
}

// Load all objects from a YAML file.
func (y *File) Load() error {
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

		y.Objects = append(y.Objects, &Object{
			File:   y,
			Object: obj,
		})
	}

	return nil
}

// Serialize all objects to a file, or remove the file if there are no objects
// left.
func (y *File) Dump() error {
	encoder := json.NewYAMLSerializer(json.DefaultMetaFactory, nil, nil)

	if len(y.Objects) == 0 {
		if _, err := os.Stat(y.Path); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}

		log.Println("Removing empty file from repo", y.Path)
		return os.Remove(y.Path)
	}

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

		meta := util.GetMeta(obj.Object)
		log.Println("Dumping", meta.GetName(), "to file", y.Path)

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
