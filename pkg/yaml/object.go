package yaml

import (
	"io"
	"github.com/justinbarrick/git-controller/pkg/util"
	"k8s.io/apimachinery/pkg/runtime"
)

// Stores a reference to an object and a file so that the object can be manipulated.
type Object struct {
	File   *File
	Object runtime.Object
}

// Return the name of the object as a string.
func (o *Object) Name() string {
	if o.Object == nil {
		return ""
	}

	meta := util.GetMeta(o.Object)
	if meta == nil {
		return ""
	}

	return meta.GetName()
}

// Return true if the version/kind/name/namespace of obj match the object
// in the Object.
func (o *Object) Matches(obj runtime.Object) bool {
	actualMeta := util.GetMeta(o.Object)
	expectedMeta := util.GetMeta(obj)
	actualType := util.GetType(o.Object)
	expectedType := util.GetType(obj)

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

// Override the object with a new version.
func (o *Object) SetObject(obj runtime.Object) {
	o.Object = obj
}

// Delete the object from the file it is in.
func (o *Object) Delete() error {
	o.File.RemoveResource(o)
	err := o.File.Dump()
	o.File = nil
	return err
}

// Save the object to disk.
func (o *Object) Save() error {
	return o.File.Dump()
}

func (o *Object) Marshal(w io.Writer) error {
	return util.MarshalObject(o.Object, w)
}
