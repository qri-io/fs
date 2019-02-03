package qfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"strings"
)

var (
	// ErrNotDirectory is the result of attempting to perform "directory-like" operations on a file
	ErrNotDirectory = errors.New("This file is not a directory")
	// ErrNotFile is the result of attempting to perform "file like" operations on a directory
	ErrNotFile = errors.New("This is a directory")
)

// File is an interface that provides functionality for handling
// files/directories as values that can be supplied to commands. For
// directories, child files are accessed serially by calling `NextFile()`.
type File interface {
	// Files implement ReadCloser, but can only be read from or closed if
	// they are not directories
	io.ReadCloser

	// FileName returns a filename associated with this file
	// TODO (b5): consider renaming this to Base
	FileName() string

	// FullPath returns the full path used when adding this file
	FullPath() string

	// IsDirectory returns true if the File is a directory (and therefore
	// supports calling `NextFile`) and false if the File is a normal file
	// (and therefor supports calling `Read` and `Close`)
	IsDirectory() bool

	// NextFile returns the next child file available (if the File is a
	// directory). It will return (nil, io.EOF) if no more files are
	// available. If the file is a regular file (not a directory), NextFile
	// will return a non-nil error.
	NextFile() (File, error)
}

// type StatFile interface {
// 	File
// 	Stat() os.FileInfo
// }

// type PeekFile interface {
// 	SizeFile
// 	Peek(n int) File
// 	Length() int
// }

// type SizeFile interface {
// 	File
// 	Size() (int64, error)
// }

// type FileInfo interface {
// 	AbsPath() string
// 	Stat() os.FileInfo
// }

// PathSetter adds the capacity to modify a path property
type PathSetter interface {
	SetPath(path string)
}

// Walk traverses a file tree calling visit on each node
func Walk(root File, depth int, visit func(f File, depth int) error) (err error) {
	if err := visit(root, depth); err != nil {
		return err
	}

	if root.IsDirectory() {
		for {
			f, err := root.NextFile()
			if err != nil {
				if err.Error() == "EOF" {
					break
				} else {
					return err
				}
			}

			if err := Walk(f, depth+1, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// Memfile is an in-memory file
type Memfile struct {
	buf  io.Reader
	path string
}

// Confirm that Memfile satisfies the File interface
var _ = (File)(&Memfile{})

// NewMemfileReader creates a file from an io.Reader
func NewMemfileReader(path string, r io.Reader) *Memfile {
	return &Memfile{
		buf:  r,
		path: path,
	}
}

// NewMemfileBytes creates a file from a byte slice
func NewMemfileBytes(path string, data []byte) *Memfile {
	return &Memfile{
		buf:  bytes.NewBuffer(data),
		path: path,
	}
}

// Read implements the io.Reader interface
func (m Memfile) Read(p []byte) (int, error) {
	return m.buf.Read(p)
}

// Close closes the file, if the backing reader implements the io.Closer interface
// it will call close on the backing Reader
func (m Memfile) Close() error {
	if closer, ok := m.buf.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// FileName returns the base of File's internal path
func (m Memfile) FileName() string {
	return filepath.Base(m.path)
}

// FullPath returns the entire path string
func (m Memfile) FullPath() string {
	return m.path
}

// SetPath implements the PathSetter interface
func (m *Memfile) SetPath(path string) {
	m.path = path
}

// IsDirectory always returns false 'cause Memfile is a file
func (Memfile) IsDirectory() bool {
	return false
}

// NextFile does nothing 'cuse Memfile isn't a directory
func (Memfile) NextFile() (File, error) {
	return nil, ErrNotDirectory
}

// Memdir is an in-memory directory
// Currently it only supports either Memfile & Memdir as links
type Memdir struct {
	path  string
	fi    int // file index for reading
	links []File
}

// Confirm that Memdir satisfies the File interface
var _ = (File)(&Memdir{})

// NewMemdir creates a new Memdir, supplying zero or more links
func NewMemdir(path string, links ...File) *Memdir {
	m := &Memdir{
		path:  path,
		links: []File{},
	}
	m.AddChildren(links...)
	return m
}

// Read does nothing, exists so MemDir implements the File interface
func (Memdir) Read([]byte) (int, error) {
	return 0, ErrNotFile
}

// Close does nothing, exists so MemDir implements the File interface
func (Memdir) Close() error {
	return ErrNotFile
}

// FileName returns the base of File's internal path
func (m Memdir) FileName() string {
	return filepath.Base(m.path)
}

// FullPath returns the entire path string
func (m Memdir) FullPath() string {
	return m.path
}

// IsDirectory returns true to indicate MemDir is a Directory
func (Memdir) IsDirectory() bool {
	return true
}

// NextFile iterates through each File in the directory on successive calls to File
// Returning io.EOF when no files remain
func (m *Memdir) NextFile() (File, error) {
	if m.fi >= len(m.links) {
		m.fi = 0
		return nil, io.EOF
	}
	defer func() { m.fi++ }()
	return m.links[m.fi], nil
}

// SetPath implements the PathSetter interface
func (m *Memdir) SetPath(path string) {
	m.path = path
	for _, f := range m.links {
		if fps, ok := f.(PathSetter); ok {
			fps.SetPath(filepath.Join(path, f.FileName()))
		}
	}
}

// AddChildren allows any sort of file to be added, but only
// implementations that implement the PathSetter interface will have
// properly configured paths.
func (m *Memdir) AddChildren(fs ...File) {
	for _, f := range fs {
		if fps, ok := f.(PathSetter); ok {
			fps.SetPath(filepath.Join(m.FullPath(), f.FileName()))
		}
		dir := m.MakeDirP(f)
		dir.links = append(dir.links, f)
	}
}

// ChildDir returns a child directory at dirname
func (m *Memdir) ChildDir(dirname string) *Memdir {
	if dirname == "" || dirname == "." || dirname == "/" {
		return m
	}
	for _, f := range m.links {
		if dir, ok := f.(*Memdir); ok {
			if filepath.Base(dir.path) == dirname {
				return dir
			}
		}
	}
	return nil
}

// MakeDirP ensures all directories specified by the given file exist, returning
// the deepest directory specified
func (m *Memdir) MakeDirP(f File) *Memdir {
	dirpath, _ := filepath.Split(f.FullPath())
	if dirpath == "" || dirpath == "/" {
		return m
	}
	dirs := strings.Split(dirpath[1:len(dirpath)-1], "/")
	if len(dirs) == 1 {
		return m
	}

	dir := m
	for _, dirname := range dirs {
		if ch := dir.ChildDir(dirname); ch != nil {
			dir = ch
			continue
		}
		ch := NewMemdir(filepath.Join(dir.FullPath(), dirname))
		dir.links = append(dir.links, ch)
		dir = ch
	}
	return dir
}

// FileString is a utility function that consumes a file, returning a sctring of file
// byte contents. This is for debugging purposes only, and should never be used for-realsies,
// as it pulls the *entire* file into a byte slice
func FileString(f File) (File, string) {
	if f.IsDirectory() {
		return f, fmt.Sprintf("directory: %s", f.FullPath())
	}

	data, err := ioutil.ReadAll(f)
	if err != nil {
		data = []byte(fmt.Sprintf("reading file: %s", err.Error()))
	}
	return NewMemfileBytes(f.FullPath(), data), string(data)
}
