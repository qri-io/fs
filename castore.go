// cafs is a "content-addressed-file-systen", which is a generalized interface for
// working with content-addressed filestores.
// real-on-the-real, this is a wrapper for IPFS.
// It looks a lot like the ipfs datastore interface, except the datastore itself
// determines keys.
package cafs

import (
	"github.com/ipfs/go-datastore"
	"github.com/ipfs/go-ipfs/commands/files"
)

// Filestore is an interface for working with a content-addressed file system.
// This interface is under active development, expect it to change lots.
// It's currently form-fitting around IPFS (ipfs.io), with far-off plans to generalize
// toward compatibility with git (git-scm.com), then maybe other stuff, who knows.
type Filestore interface {
	// put places a raw slice of bytes. Expect this to change to something like:
	// Put(file files.File, options map[string]interface{}) (key datastore.Key, err error)
	// The most notable difference from a standard file store is the store itself determines
	// the resulting key (google "content addressing" for more info ;)
	Put(value []byte, pin bool) (key datastore.Key, err error)

	// NewAdder should allocate an Adder instance for adding files to the filestore
	// Adder gives the highest degree of control over the file adding process at the
	// cost of being harder to work with.
	// "pin" is a flag for recursively pinning this object
	// "wrap" sets weather the top level should be wrapped in a directory
	// expect this to change to something like:
	// NewAdder(opt map[string]interface{}) (Adder, error)
	NewAdder(pin, wrap bool) (Adder, error)

	// Get retrieves the object `value` named by `key`.
	// Get will return ErrNotFound if the key is not mapped to a value.
	Get(key datastore.Key) (data []byte, err error)

	// Has returns whether the `key` is mapped to a `value`.
	// In some contexts, it may be much cheaper only to check for existence of
	// a value, rather than retrieving the value itself. (e.g. HTTP HEAD).
	// The default implementation is found in `GetBackedHas`.
	Has(key datastore.Key) (exists bool, err error)

	// Delete removes the value for given `key`.
	Delete(key datastore.Key) error
}

// TODO - This is an in-progress interface upgrade for content stores that support
// the concept of pinning (originated by IPFS).
// *currently not in use, and not implemented by anyone, ever*
type Pinner interface {
	Pin(key datastore.Key, recursive bool) error
	Unpin(key datastore.Key, recursive bool) error
}

// Adder is the interface for adding files to a Filestore. The addition process
// is parallelized. Implementers must make all required AddFile calls, then call
// Close to finalize the addition process. Progress can be monitored through the
// Added() channel
type Adder interface {
	// AddFile adds a file or directory of files to the store
	// this function will return immideately, consumers should read
	// from the Added() channel to see the results of file addition.
	AddFile(files.File) error
	// Added gives a channel to read added files from.
	Added() chan AddedFile
	// In IPFS land close calls adder.Finalize() and adder.PinRoot()
	// (files will only be pinned if the pin flag was set on NewAdder)
	// Close will close the underlying
	Close() error
}

// AddedFile reports on the results of adding a file to the store
// TODO - add filepath to this struct
type AddedFile struct {
	Name  string
	Bytes int64
	Hash  string
	Size  string
}
