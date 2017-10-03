package ipfs_filestore

import (
	"context"
	"fmt"

	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	datastore "github.com/ipfs/go-datastore"
	blockservice "github.com/ipfs/go-ipfs/blockservice"
	files "github.com/ipfs/go-ipfs/commands/files"
	core "github.com/ipfs/go-ipfs/core"
	corerepo "github.com/ipfs/go-ipfs/core/corerepo"
	coreunix "github.com/ipfs/go-ipfs/core/coreunix"
	dag "github.com/ipfs/go-ipfs/merkledag"
	path "github.com/ipfs/go-ipfs/path"
	tar "github.com/ipfs/go-ipfs/thirdparty/tar"
	uarchive "github.com/ipfs/go-ipfs/unixfs/archive"
	cafs "github.com/qri-io/cafs"
	memfs "github.com/qri-io/cafs/memfs"

	ipfsds "gx/ipfs/QmVSase1JP7cq9QkPT46oNwdp9pT6kBkG3oqS14y3QcZjG/go-datastore"
)

type Filestore struct {
	node *core.IpfsNode
}

func NewFilestore(config ...func(cfg *StoreCfg)) (*Filestore, error) {
	cfg := DefaultConfig()
	for _, option := range config {
		option(cfg)
	}

	if cfg.Node != nil {
		return &Filestore{
			node: cfg.Node,
		}, nil
	}

	if err := cfg.InitRepo(); err != nil {
		return nil, err
	}

	node, err := core.NewNode(cfg.Ctx, &cfg.BuildCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating ipfs node: %s\n", err.Error())
	}

	return &Filestore{
		node: node,
	}, nil
}

func (fs *Filestore) Node() *core.IpfsNode {
	return fs.node
}

func (fs *Filestore) Has(key datastore.Key) (exists bool, err error) {
	ipfskey := ipfsds.NewKey(key.String())
	return fs.Node().Repo.Datastore().Has(ipfskey)
}

func (fs *Filestore) Get(key datastore.Key) (files.File, error) {
	// fs.Node().Repo.Datastore().Get(key)
	return fs.getKey(key)
}

func (fs *Filestore) Fetch(source cafs.Source, key datastore.Key) (files.File, error) {
	return fs.getKey(key)
}

func (fs *Filestore) Put(file files.File, pin bool) (key datastore.Key, err error) {
	data, err := ioutil.ReadAll(file)
	if err != nil {
		return datastore.NewKey(""), err
	}

	hash, err := fs.AddBytes(data, pin)
	if err != nil {
		return datastore.NewKey(""), err
	}
	return datastore.NewKey("/ipfs/" + hash), nil
}

func (fs *Filestore) Delete(path datastore.Key) error {
	// TODO - formally remove files?
	return fs.Unpin(path, true)
}

func (fs *Filestore) getKey(key datastore.Key) (files.File, error) {
	p := path.Path(key.String())
	node := fs.node

	// TODO - we'll need a "local" list for this to work properly
	// currently this thing is *always* going to check the d.web for
	// a hash if it's online, which is a behaviour we need control over
	// might be worth expanding the cafs interface with the concept of
	// remote gets
	dn, err := core.Resolve(node.Context(), node.Namesys, node.Resolver, p)
	if err != nil {
		fmt.Println("resolver error")
		return nil, err
	}

	// fmt.Println()
	// switch dn := dn.(type) {
	//   case *dag.ProtoNode:
	//     size, err := dn.Size()
	//     if err != nil {
	//       res.SetError(err, cmds.ErrNormal)
	//       return
	//     }
	//     res.SetLength(size)
	//   case *dag.RawNode:
	//     res.SetLength(uint64(len(dn.RawData())))
	//   default:
	//     res.SetError(fmt.Errorf("'ipfs get' only supports unixfs nodes"), cmds.ErrNormal)
	//     return
	//   }

	rdr, err := uarchive.DagArchive(node.Context(), dn, p.String(), node.DAG, false, 0)
	if err != nil {
		return nil, err
	}

	fp := filepath.Join("/tmp", key.BaseNamespace())

	e := tar.Extractor{
		Path:     fp,
		Progress: func(int64) int64 { return 0 },
	}
	if err := e.Extract(rdr); err != nil {
		return nil, err
	}

	f, err := os.Open(fp)
	if err != nil {
		return nil, err
	}

	return memfs.NewMemfileReader(key.String(), f), nil
}

// Adder wraps a coreunix adder to conform to the cafs adder interface
type Adder struct {
	adder *coreunix.Adder
	out   chan interface{}
	added chan cafs.AddedFile
}

func (a *Adder) AddFile(f files.File) error {
	return a.adder.AddFile(f)
}
func (a *Adder) Added() chan cafs.AddedFile {
	return a.added
}

func (a *Adder) Close() error {
	defer close(a.out)
	if _, err := a.adder.Finalize(); err != nil {
		return err
	}
	return a.adder.PinRoot()
}

func (fs *Filestore) NewAdder(pin, wrap bool) (cafs.Adder, error) {
	node := fs.Node()
	ctx := context.Background()
	bserv := blockservice.New(node.Blockstore, node.Exchange)
	dagserv := dag.NewDAGService(bserv)

	a, err := coreunix.NewAdder(ctx, node.Pinning, node.Blockstore, dagserv)
	if err != nil {
		return nil, err
	}

	outChan := make(chan interface{}, 8)
	added := make(chan cafs.AddedFile, 8)
	a.Out = outChan
	a.Pin = pin
	a.Wrap = wrap

	go func() {
		for {
			select {
			case out, ok := <-outChan:
				if ok {
					output := out.(*coreunix.AddedObject)
					if len(output.Hash) > 0 {
						// fmt.Println(output.Name, output.Size, output.Bytes, output.Hash)
						added <- cafs.AddedFile{
							Path:  datastore.NewKey("/ipfs/" + output.Hash),
							Name:  output.Name,
							Hash:  output.Hash,
							Bytes: output.Bytes,
							Size:  output.Size,
						}
					}
				} else {
					close(added)
					return
				}
			case <-ctx.Done():
				close(added)
				return
			}
		}
	}()

	return &Adder{
		adder: a,
		out:   outChan,
		added: added,
	}, nil
}

func (fs *Filestore) AddPath(path string, pin bool) (hash string, err error) {
	node := fs.Node()

	ctx := context.Background()
	bserv := blockservice.New(node.Blockstore, node.Exchange)
	dagserv := dag.NewDAGService(bserv)

	fileAdder, err := coreunix.NewAdder(ctx, node.Pinning, node.Blockstore, dagserv)
	if err != nil {
		return
	}

	fi, err := os.Stat(path)
	if err != nil {
		return
	}

	rfile, err := files.NewSerialFile("", path, false, fi)
	if err != nil {
		return
	}

	outChan := make(chan interface{}, 8)
	defer close(outChan)

	fileAdder.Out = outChan
	if err = fileAdder.AddFile(rfile); err != nil {
		return
	}

	if _, err = fileAdder.Finalize(); err != nil {
		return
	}

	if pin {
		if err = fileAdder.PinRoot(); err != nil {
			return
		}
	}

	for {
		select {
		case out, ok := <-outChan:
			if ok {
				output := out.(*coreunix.AddedObject)
				if len(output.Hash) > 0 {
					hash = output.Hash
					return
				}
			}
		}
	}

	err = fmt.Errorf("something's gone horribly wrong")
	return
}

// AddAndPinBytes adds a file to the top level IPFS Node
func (fs *Filestore) AddBytes(data []byte, pin bool) (hash string, err error) {
	node := fs.Node()

	ctx := context.Background()
	bserv := blockservice.New(node.Blockstore, node.Exchange)
	dagserv := dag.NewDAGService(bserv)

	fileAdder, err := coreunix.NewAdder(ctx, node.Pinning, node.Blockstore, dagserv)
	fileAdder.Pin = pin
	if err != nil {
		return
	}

	path := filepath.Join("/tmp", time.Now().String())

	if err = ioutil.WriteFile(path, data, os.ModePerm); err != nil {
		return
	}

	fi, err := os.Stat(path)
	if err != nil {
		return
	}

	rfile, err := files.NewSerialFile("", path, false, fi)
	if err != nil {
		return
	}

	outChan := make(chan interface{}, 8)
	defer close(outChan)

	fileAdder.Out = outChan

	if err = fileAdder.AddFile(rfile); err != nil {
		return
	}
	if _, err = fileAdder.Finalize(); err != nil {
		return
	}
	if err = fileAdder.PinRoot(); err != nil {
		return
	}

	for {
		select {
		case out, ok := <-outChan:
			if ok {
				output := out.(*coreunix.AddedObject)
				if len(output.Hash) > 0 {
					hash = output.Hash
					return
				}
			}
		}
	}

	err = fmt.Errorf("something's gone horribly wrong")
	return
}

func (fs *Filestore) Pin(path datastore.Key, recursive bool) error {
	_, err := corerepo.Pin(fs.node, fs.node.Context(), []string{path.String()}, recursive)
	return err
}

func (fs *Filestore) Unpin(path datastore.Key, recursive bool) error {
	_, err := corerepo.Unpin(fs.node, fs.node.Context(), []string{path.String()}, recursive)
	return err
}
