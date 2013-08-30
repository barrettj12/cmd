// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"path/filepath"

	"launchpad.net/gnuflag"

	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/environs"
	"launchpad.net/juju-core/environs/localstorage"
	"launchpad.net/juju-core/environs/simplestreams"
	"launchpad.net/juju-core/environs/tools"
	coretools "launchpad.net/juju-core/tools"
	"launchpad.net/juju-core/utils"
	"launchpad.net/juju-core/version"
)

// pathPrefix is the prefix for metadata paths.
const pathPrefix = "tools/"

// ToolsMetadataCommand is used to generate simplestreams metadata for
// juju tools.
type ToolsMetadataCommand struct {
	cmd.EnvCommandBase
	fetch       bool
	metadataDir string
}

func (c *ToolsMetadataCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "generate-tools",
		Purpose: "generate simplestreams tools metadata",
	}
}

func (c *ToolsMetadataCommand) SetFlags(f *gnuflag.FlagSet) {
	c.EnvCommandBase.SetFlags(f)
	f.BoolVar(&c.fetch, "fetch", true, "fetch tools and compute content size and hash")
	f.StringVar(&c.metadataDir, "d", "", "local directory to locate tools and store metadata")
	// TODO(axw) allow user to specify version
}

func (c *ToolsMetadataCommand) Run(context *cmd.Context) error {
	env, err := environs.NewFromName(c.EnvName)
	if err != nil {
		return err
	}

	if c.metadataDir != "" {
		c.metadataDir = utils.NormalizePath(c.metadataDir)
		listener, err := localstorage.Serve("127.0.0.1:0", c.metadataDir)
		if err != nil {
			return err
		}
		defer listener.Close()
		storageAddr := listener.Addr().String()
		env = localdirEnv{env, localstorage.Client(storageAddr)}
	}

	fmt.Fprintln(context.Stdout, "Finding tools...")
	toolsList, err := tools.FindTools(env, version.Current.Major, coretools.Filter{})
	if err != nil {
		return err
	}

	metadata := make([]*tools.ToolsMetadata, len(toolsList))
	for i, t := range toolsList {
		u, err := url.Parse(t.URL)
		if err != nil {
			return err
		}
		urlPath := u.Path[1:]
		// FIXME(axw) path should be relative to base URL. We don't know whether
		// it's from the public or private storage at this point.

		var size int64
		var sha256hex string
		if c.fetch {
			fmt.Fprintln(context.Stdout, "Fetching tools to generate hash:", t.URL)
			var sha256hash hash.Hash
			size, sha256hash, err = fetchToolsHash(t.URL)
			if err != nil {
				return err
			}
			sha256hex = fmt.Sprintf("%x", sha256hash.Sum(nil))
		}

		metadata[i] = &tools.ToolsMetadata{
			Release:  t.Version.Series,
			Version:  t.Version.Number.String(),
			Arch:     t.Version.Arch,
			Path:     urlPath,
			FileType: "tar.gz",
			Size:     size,
			SHA256:   sha256hex,
		}
	}

	index, products, err := tools.MarshalToolsMetadataJSON(metadata)
	if err != nil {
		return err
	}
	objects := []struct {
		path string
		data []byte
	}{
		{pathPrefix + simplestreams.DefaultIndexPath + simplestreams.UnsignedSuffix, index},
		{pathPrefix + tools.ProductMetadataPath, products},
	}
	for _, object := range objects {
		var path string
		if c.metadataDir != "" {
			path = filepath.Join(c.metadataDir, object.path)
		} else {
			objectUrl, err := env.Storage().URL(object.path)
			if err != nil {
				return err
			}
			path = objectUrl
		}
		fmt.Fprintf(context.Stdout, "Writing %s\n", path)
		buf := bytes.NewBuffer(object.data)
		if err != nil {
			return err
		}
		if err = env.Storage().Put(object.path, buf, int64(buf.Len())); err != nil {
			return err
		}
	}
	return nil
}

// fetchToolsHash fetches the file at the specified URL,
// and calculates its size in bytes and computes a SHA256
// hash of its contents.
func fetchToolsHash(url string) (size int64, sha256hash hash.Hash, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return 0, nil, err
	}
	sha256hash = sha256.New()
	size, err = io.Copy(sha256hash, resp.Body)
	resp.Body.Close()
	return size, sha256hash, err
}

// localdirEnv wraps an Environ, returning a localstorage Storage for its
// private storage.
type localdirEnv struct {
	environs.Environ
	storage environs.Storage
}

func (e localdirEnv) Storage() environs.Storage {
	return e.storage
}
