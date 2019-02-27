package yaml

import (
	"bytes"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"bufio"
	"github.com/justinbarrick/git-controller/pkg/util"
	"io"
	"k8s.io/apimachinery/pkg/util/yaml"
	"gopkg.in/src-d/go-billy.v4"
	"os"
	"path/filepath"
)

// Allows manipulation of objects in a YAML file without disturbing the other
// objects.
type File struct {
	Objects []*Object
	Path    string
	fs      billy.Filesystem
}

// Instantiate a new YAML file.
func NewFile(fs billy.Filesystem, path string) *File {
	return &File{
		fs:      fs,
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
			meta := util.GetMeta(obj.Object)
			kind := util.GetType(obj.Object)

			util.Log.Info("pruning resource", "name", meta.GetName(), "namespace",
			              meta.GetNamespace(), "kind", kind.Kind)
			continue
		}

		objects = append(objects, object)
	}

	y.Objects = objects
}

// Load all objects from a YAML file.
func (y *File) Load() ([]*Object, error) {
	opened, err := y.fs.Open(y.Path)
	if err != nil {
		return nil, err
	}
	defer opened.Close()

	yamlReader := yaml.NewYAMLReader(bufio.NewReader(opened))

	for {
		data, err := yamlReader.Read()
		if err != nil {
			if err == io.EOF {
				return y.Objects, nil
			}
			return nil, err
		}

		obj := &unstructured.Unstructured{}
		decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewBuffer(data), len(data))

		if err = decoder.Decode(obj); err != nil {
			return nil, err
		}

		y.Objects = append(y.Objects, &Object{
			File:   y,
			Object: obj,
		})
	}

	return y.Objects, nil
}

// Serialize all objects to a file, or remove the file if there are no objects
// left.
func (y *File) Dump() error {
	if len(y.Objects) == 0 {
		if _, err := y.fs.Stat(y.Path); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}

		util.Log.Info("deleting empty file", "path", y.Path)
		return y.fs.Remove(y.Path)
	}

	if err := y.fs.MkdirAll(filepath.Dir(y.Path), 0700); err != nil {
		return err
	}

	outFile, err := y.fs.Create(y.Path)
	if err != nil {
		return err
	}

	defer outFile.Close()

	for index, obj := range y.Objects {
		if index != 0 {
			outFile.Write([]byte("---\n"))
		}

		meta := util.GetMeta(obj.Object)
		kind := util.GetType(obj.Object)

		util.Log.Info("saving object", "name", meta.GetName(), "path", y.Path,
		              "kind", kind.Kind, "namespace", meta.GetNamespace())
		if err := obj.Marshal(outFile); err != nil {
			return err
		}
	}

	return nil
}
