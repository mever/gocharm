package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/juju/charm.v4"
	"launchpad.net/errgo/errors"
)

// writeHooks ensures that the charm has the given set of hooks.
// TODO write install and start hooks even if they're not registered,
// because otherwise it won't be treated as a valid charm.
func writeHooks(dir *charm.CharmDir, hooks map[string]bool) error {
	if *verbose {
		log.Printf("writing hooks in %s", dir.Path)
	}
	hookDir := filepath.Join(dir.Path, "hooks")
	if err := os.MkdirAll(hookDir, 0777); err != nil {
		return errors.Wrapf(err, "failed to make hooks directory")
	}
	infos, err := ioutil.ReadDir(hookDir)
	if err != nil {
		return errors.Wrap(err)
	}
	if *verbose {
		log.Printf("found %d existing hooks", len(infos))
	}
	// Remove any hooks in the directory that are not registered,
	// but only if their contents are exactly the same as expected,
	// to avoid losing user-level changes.
	found := make(map[string]bool)
	for _, info := range infos {
		hookPath := filepath.Join(hookDir, info.Name())
		if (info.Mode() & os.ModeType) != 0 {
			if *verbose {
				log.Printf("ignoring non-file %s", hookPath)
			}
			continue
		}
		sameContents, contentsErr := fileHasContents(hookPath, hookStub(info.Name()))
		if hooks[info.Name()] {
			if contentsErr != nil {
				return errors.Wrapf(err, "cannot replace %q")
			}
			if !sameContents {
				return errors.Newf("cannot replace %q because it has unexpected contents", hookPath)
			}
			if *verbose {
				log.Printf("found existing hook %s", hookPath)
			}
			found[info.Name()] = true
		} else {
			if contentsErr != nil {
				warningf("not removing %q", contentsErr)
				continue
			}
			if !sameContents {
				warningf("not removing %q because it has unexpected contents", hookPath)
				continue
			}
			if *verbose {
				log.Printf("removing old hook %s", hookPath)
			}
			if err := os.Remove(hookPath); err != nil {
				return errors.Wrap(err)
			}
		}
	}
	// Add any new hooks we need to the charm directory.
	for hookName := range hooks {
		hookPath := filepath.Join(hookDir, hookName)
		if !found[hookName] {
			if *verbose {
				log.Printf("creating new hook %s", hookPath)
			}
			if err := ioutil.WriteFile(hookPath, hookStub(hookName), 0755); err != nil {
				return errors.Wrap(err)
			}
		}
	}
	return nil
}

// fileHasContents reports whether the file at the given path
// has the given contents. It returns an os.IsNotExist error if the
// file does not exist.
func fileHasContents(path string, contents []byte) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, errors.Wrap(err, os.IsNotExist)
	}
	defer f.Close()
	buf := make([]byte, len(contents)+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return false, errors.Wrap(err)
	}
	if !bytes.Equal(buf[:n], contents) {
		return false, nil
	}
	return true, nil
}

func registeredCharmInfo(dir *charm.CharmDir) (*charmInfo, error) {
	err := compile(dir, "inspect", inspectCode, false)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot build hook inspection code")
	}
	inspectPath := filepath.Join(dir.Path, "bin", "inspect")
	defer os.Remove(inspectPath)
	c := exec.Command(inspectPath)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return nil, errors.Wrapf(err, "failed to run inspect")
	}
	var out charmInfo
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return nil, errors.Wrapf(err, "cannot unmarshal %q", err)
	}
	if len(out.Hooks) == 0 {
		return nil, errors.New("no hooks registered")
	}
	if *verbose {
		log.Printf("registered hooks: %v", out.Hooks)
		log.Printf("%d registered relations", len(out.Relations))
		log.Printf("%d registered config options", len(out.Config))
	}
	return &out, nil
}

const hookStubTemplate = `#!/bin/sh
$CHARM_DIR/bin/runhook %s
`

func hookStub(hookName string) []byte {
	return []byte(fmt.Sprintf(hookStubTemplate, hookName))
}

// charmInfo holds the information we glean
// from inspecting the hook registry.
// Note that this must be kept in sync with the
// version in inspectCode below.
type charmInfo struct {
	Hooks     map[string]bool
	Relations map[string]charm.Relation
	Config    map[string]charm.Option
}

const inspectCode = `
// This file is automatically generated. Do not edit.

package main

import (
	"encoding/json"
	"` + hookPackage + `"
	"gopkg.in/juju/charm.v4"
	"os"
	runhook "runhook"
)

// charmInfo must be kept in sync with the charmInfo
// type above.
type charmInfo struct {
	Hooks     map[string]bool
	Relations map[string]charm.Relation
	Config    map[string]charm.Option
}

func main() {
	r := hook.NewRegistry()
	runhook.RegisterHooks(r)
	hookMap := make(map[string]bool)
	for _, hook := range r.RegisteredHooks() {
		hookMap[hook] = true
	}
	data, err := json.Marshal(charmInfo{
		Hooks:     hookMap,
		Relations: r.RegisteredRelations(),
		Config:    r.RegisteredConfig(),
	})
	if err != nil {
		panic(err)
	}
	os.Stdout.Write(data)
}
`
