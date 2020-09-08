package directory

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cppforlife/go-cli-ui/ui"
	ctlconf "github.com/k14s/vendir/pkg/vendir/config"
	dircopy "github.com/otiai10/copy"
)

type Directory struct {
	opts ctlconf.Directory
	ui   ui.UI
}

func NewDirectory(opts ctlconf.Directory, ui ui.UI) *Directory {
	return &Directory{opts, ui}
}

var (
	tmpDir         = ".vendir-tmp"
	stagingTmpDir  = filepath.Join(tmpDir, "staging")
	incomingTmpDir = filepath.Join(tmpDir, "incoming")
)

type SyncOpts struct {
	RefFetcher     RefFetcher
	GithubAPIToken string
	HelmBinary     string
}

func (d *Directory) Sync(syncOpts SyncOpts) (ctlconf.LockDirectory, error) {
	lockConfig := ctlconf.LockDirectory{Path: d.opts.Path}

	err := d.cleanUpTmpDir()
	if err != nil {
		return lockConfig, err
	}

	defer d.cleanUpTmpDir()

	err = os.MkdirAll(stagingTmpDir, 0700)
	if err != nil {
		return lockConfig, fmt.Errorf("Creating staging dir '%s': %s", stagingTmpDir, err)
	}

	err = os.MkdirAll(incomingTmpDir, 0700)
	if err != nil {
		return lockConfig, fmt.Errorf("Creating incoming dir '%s': %s", incomingTmpDir, err)
	}

	for _, contents := range d.opts.Contents {
		stagingDstPath := filepath.Join(stagingTmpDir, contents.Path)
		stagingDstPathParent := filepath.Dir(stagingDstPath)

		err := os.MkdirAll(stagingDstPathParent, 0700)
		if err != nil {
			return lockConfig, fmt.Errorf("Creating directory '%s': %s", stagingDstPathParent, err)
		}

		switch {
		case contents.Git != nil:
			d.ui.PrintLinef("%s + %s (git from %s@%s)",
				d.opts.Path, contents.Path, contents.Git.URL, contents.Git.Ref)

			gitLockConf, err := GitSync{*contents.Git, d.ui}.Sync(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Syncing directory '%s' with git contents: %s", contents.Path, err)
			}

			err = FileFilter{contents}.Apply(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Filtering paths in directory '%s': %s", contents.Path, err)
			}

			lockConfig.Contents = append(lockConfig.Contents, ctlconf.LockDirectoryContents{
				Path: contents.Path,
				Git:  &gitLockConf,
			})

		case contents.HTTP != nil:
			d.ui.PrintLinef("%s + %s (http from %s)", d.opts.Path, contents.Path, contents.HTTP.URL)

			httpLockConf, err := (&HTTPSync{*contents.HTTP, syncOpts.RefFetcher}).Sync(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Syncing directory '%s' with HTTP contents: %s", contents.Path, err)
			}

			err = FileFilter{contents}.Apply(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Filtering paths in directory '%s': %s", contents.Path, err)
			}

			lockConfig.Contents = append(lockConfig.Contents, ctlconf.LockDirectoryContents{
				Path: contents.Path,
				HTTP: &httpLockConf,
			})

		case contents.Image != nil:
			d.ui.PrintLinef("%s + %s (image from %s)", d.opts.Path, contents.Path, contents.Image.URL)

			imageLockConf, err := NewImageSync(*contents.Image, syncOpts.RefFetcher).Sync(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Syncing directory '%s' with image contents: %s", contents.Path, err)
			}

			err = FileFilter{contents}.Apply(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Filtering paths in directory '%s': %s", contents.Path, err)
			}

			lockConfig.Contents = append(lockConfig.Contents, ctlconf.LockDirectoryContents{
				Path:  contents.Path,
				Image: &imageLockConf,
			})

		case contents.GithubRelease != nil:
			sync := GithubReleaseSync{*contents.GithubRelease, syncOpts.GithubAPIToken, d.ui}

			desc, _, _ := sync.DescAndURL()
			d.ui.PrintLinef("%s + %s (github release %s)", d.opts.Path, contents.Path, desc)

			lockConf, err := sync.Sync(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Syncing directory '%s' with github release contents: %s", contents.Path, err)
			}

			err = FileFilter{contents}.Apply(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Filtering paths in directory '%s': %s", contents.Path, err)
			}

			lockConfig.Contents = append(lockConfig.Contents, ctlconf.LockDirectoryContents{
				Path:          contents.Path,
				GithubRelease: &lockConf,
			})

		case contents.HelmChart != nil:
			helmChartSync := NewHelmChart(*contents.HelmChart, syncOpts.HelmBinary, syncOpts.RefFetcher)

			d.ui.PrintLinef("%s + %s (helm chart from %s)",
				d.opts.Path, contents.Path, helmChartSync.Desc())

			chartLockConf, err := helmChartSync.Sync(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Syncing directory '%s' with helm chart contents: %s", contents.Path, err)
			}

			err = FileFilter{contents}.Apply(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Filtering paths in directory '%s': %s", contents.Path, err)
			}

			lockConfig.Contents = append(lockConfig.Contents, ctlconf.LockDirectoryContents{
				Path:      contents.Path,
				HelmChart: &chartLockConf,
			})

		case contents.Manual != nil:
			d.ui.PrintLinef("%s + %s (manual)", d.opts.Path, contents.Path)

			srcPath := filepath.Join(d.opts.Path, contents.Path)

			err := os.Rename(srcPath, stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Moving directory '%s' to staging dir: %s", srcPath, err)
			}

			lockConfig.Contents = append(lockConfig.Contents, ctlconf.LockDirectoryContents{
				Path:   contents.Path,
				Manual: &ctlconf.LockDirectoryContentsManual{},
			})

		case contents.Directory != nil:
			d.ui.PrintLinef("%s + %s (directory)", d.opts.Path, contents.Path)

			err := dircopy.Copy(contents.Directory.Path, stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Copying another directory contents into directory '%s': %s", contents.Path, err)
			}

			err = FileFilter{contents}.Apply(stagingDstPath)
			if err != nil {
				return lockConfig, fmt.Errorf("Filtering paths in directory '%s': %s", contents.Path, err)
			}

			lockConfig.Contents = append(lockConfig.Contents, ctlconf.LockDirectoryContents{
				Path:      contents.Path,
				Directory: &ctlconf.LockDirectoryContentsDirectory{},
			})

		default:
			return lockConfig, fmt.Errorf("Unknown contents type for directory '%s' (known: git, manual)", contents.Path)
		}
	}

	err = os.RemoveAll(d.opts.Path)
	if err != nil {
		return lockConfig, fmt.Errorf("Deleting dir %s: %s", d.opts.Path, err)
	}

	// Clean to avoid getting 'out/in/' from 'out/in/' instead of just 'out'
	parentPath := filepath.Dir(filepath.Clean(d.opts.Path))

	err = os.MkdirAll(parentPath, 0700)
	if err != nil {
		return lockConfig, fmt.Errorf("Creating final location parent dir %s: %s", parentPath, err)
	}

	err = os.Rename(stagingTmpDir, d.opts.Path)
	if err != nil {
		return lockConfig, fmt.Errorf("Moving staging directory '%s' to final location '%s': %s", stagingTmpDir, d.opts.Path, err)
	}

	return lockConfig, nil
}

func (d *Directory) cleanUpTmpDir() error {
	err := os.RemoveAll(tmpDir)
	if err != nil {
		return fmt.Errorf("Deleting tmp dir '%s': %s", tmpDir, err)
	}
	return nil
}
