package ipfs_filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sync"

	config "github.com/ipfs/go-ipfs-config"
	"github.com/ipfs/go-ipfs/assets"
	"github.com/ipfs/go-ipfs/core"
	"github.com/ipfs/go-ipfs/namesys"
	"github.com/ipfs/go-ipfs/plugin/loader"
	"github.com/ipfs/go-ipfs/repo/fsrepo"
)

const (
	nBitsForKeypair = 2048
)

var errRepoExists = errors.New(`ipfs configuration file already exists!
Reinitializing would overwrite your keys.
`)

// InitRepo is a more specific version of the init command: github.com/ipfs/go-ipfs/cmd/ipfs/init.go
// it's adapted to let qri initialize a repo. This func should be maintained to reflect the
// ipfs master branch.
func InitRepo(repoPath, configPath string) error {
	if daemonLocked, err := fsrepo.LockedByOtherProcess(repoPath); err != nil {
		return err
	} else if daemonLocked {
		e := "ipfs daemon is running. please stop it to run this command"
		return fmt.Errorf(e)
	}

	var conf *config.Config
	if configPath != "" {
		confFile, err := os.Open(configPath)
		if err != nil {
			return fmt.Errorf("error opening configuration file: %s", err.Error())
		}
		conf = &config.Config{}
		if err := json.NewDecoder(confFile).Decode(conf); err != nil {
			// res.SetError(err, cmds.ErrNormal)
			return fmt.Errorf("invalid configuration file: %s", configPath)
		}
	}

	if err := doInit(ioutil.Discard, repoPath, false, nBitsForKeypair, nil, conf); err != nil {
		return err
	}

	return nil
}

func doInit(out io.Writer, repoRoot string, empty bool, nBitsForKeypair int, confProfiles []string, conf *config.Config) error {

	if err := checkWriteable(repoRoot); err != nil {
		return err
	}

	if fsrepo.IsInitialized(repoRoot) {
		return errRepoExists
	}
	if _, err := fmt.Fprintf(out, "initializing IPFS node at %s\n", repoRoot); err != nil {
		return err
	}

	if conf == nil {
		var err error
		conf, err = config.Init(out, nBitsForKeypair)
		if err != nil {
			return err
		}
	}

	for _, profileStr := range confProfiles {
		profile, ok := config.Profiles[profileStr]
		if !ok {
			return fmt.Errorf("invalid configuration profile: %s", profileStr)
		}

		if err := profile.Transform(conf); err != nil {
			return err
		}
	}

	if _, err := loadPluginsOnce(repoRoot); err != nil {
		return err
	}

	if err := fsrepo.Init(repoRoot, conf); err != nil {
		return err
	}

	if !empty {
		if err := addDefaultAssets(out, repoRoot); err != nil {
			return err
		}
	}

	return initializeIpnsKeyspace(repoRoot)
}

func checkWriteable(dir string) error {
	_, err := os.Stat(dir)
	if err == nil {
		// dir exists, make sure we can write to it
		testfile := path.Join(dir, "test")
		fi, err := os.Create(testfile)
		if err != nil {
			if os.IsPermission(err) {
				return fmt.Errorf("%s is not writeable by the current user", dir)
			}
			return fmt.Errorf("unexpected error while checking writeablility of repo root: %s", err)
		}
		fi.Close()
		return os.Remove(testfile)
	}

	if os.IsNotExist(err) {
		// dir doesnt exist, check that we can create it
		return os.Mkdir(dir, 0775)
	}

	if os.IsPermission(err) {
		return fmt.Errorf("cannot write to %s, incorrect permissions", err)
	}

	return err
}

func addDefaultAssets(out io.Writer, repoRoot string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := fsrepo.Open(repoRoot)
	if err != nil { // NB: repo is owned by the node
		return err
	}

	nd, err := core.NewNode(ctx, &core.BuildCfg{Repo: r})
	if err != nil {
		return err
	}
	defer nd.Close()

	dkey, err := assets.SeedInitDocs(nd)
	if err != nil {
		return fmt.Errorf("init: seeding init docs failed: %s", err)
	}
	// log.Debugf("init: seeded init docs %s", dkey)

	if _, err = fmt.Fprintf(out, "to get started, enter:\n"); err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "\n\tipfs cat /ipfs/%s/readme\n\n", dkey)
	return err
}

// func initializeIpnsKeyspace(repoRoot string) error {
// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()

// 	r, err := fsrepo.Open(repoRoot)
// 	if err != nil { // NB: repo is owned by the node
// 		return err
// 	}

// 	nd, err := core.NewNode(ctx, &core.BuildCfg{Repo: r})
// 	if err != nil {
// 		return err
// 	}
// 	defer nd.Close()

// 	// err = nd.SetupOfflineRouting()
// 	// if err != nil {
// 	// 	return err
// 	// }

// 	return namesys.InitializeKeyspace(ctx, nd.Namesys, nd.Pinning, nd.PrivateKey)
// }

func initializeIpnsKeyspace(repoRoot string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := fsrepo.Open(repoRoot)
	if err != nil { // NB: repo is owned by the node
		return err
	}

	nd, err := core.NewNode(ctx, &core.BuildCfg{Repo: r})
	if err != nil {
		return err
	}
	defer nd.Close()

	return namesys.InitializeKeyspace(ctx, nd.Namesys, nd.Pinning, nd.PrivateKey)
}

var (
	loadOnce       sync.Once
	pluginLoader   *loader.PluginLoader
	loadPluginsErr error
)

func loadPluginsOnce(repoPath string) (*loader.PluginLoader, error) {
	do := func() {
		pluginLoader, loadPluginsErr = loadPlugins(repoPath)
	}
	loadOnce.Do(do)
	return pluginLoader, loadPluginsErr
}

func loadPlugins(repoPath string) (*loader.PluginLoader, error) {
	// check if repo is accessible before loading plugins
	pluginpath := filepath.Join(repoPath, "plugins")

	var plugins *loader.PluginLoader
	ok, err := checkPermissions(repoPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		pluginpath = ""
	}
	plugins, err = loader.NewPluginLoader(pluginpath)
	if err != nil {
		return nil, fmt.Errorf("error loading plugins: %s", err)
	}

	if err := plugins.Initialize(); err != nil {
		return nil, fmt.Errorf("error initializing plugins: %s", err)
	}

	if err := plugins.Inject(); err != nil {
		return nil, fmt.Errorf("error initializing plugins: %s", err)
	}

	return plugins, nil
}

func checkPermissions(path string) (bool, error) {
	_, err := os.Open(path)
	if os.IsNotExist(err) {
		// repo does not exist yet - don't load plugins, but also don't fail
		return false, nil
	}
	if os.IsPermission(err) {
		// repo is not accessible. error out.
		return false, fmt.Errorf("error opening repository at %s: permission denied", path)
	}

	return true, nil
}
