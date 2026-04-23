package build

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"

	api "github.com/evanw/esbuild/pkg/api"
)

// BuildJS compiles web/src/main.ts into a single minified JS bundle for
// the browser. Returns the JS bytes ready to be served at /assets/main.js.
// srcDir defaults to "web/src" relative to the module root; caller can
// override for tests.
func BuildJS(srcDir string) ([]byte, error) {
	if srcDir == "" {
		srcDir = filepath.Join("web", "src")
	}

	result := api.Build(api.BuildOptions{
		EntryPoints:       []string{filepath.Join(srcDir, "main.ts")},
		Bundle:            true,
		Write:             false,
		Platform:          api.PlatformBrowser,
		Target:            api.ES2020,
		Format:            api.FormatIIFE,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		Sourcemap:         api.SourceMapNone,
		LogLevel:          api.LogLevelSilent,
		Loader:            map[string]api.Loader{".ts": api.LoaderTS},
	})

	if len(result.Errors) > 0 {
		errs := make([]error, 0, len(result.Errors))
		for _, e := range result.Errors {
			if e.Location != nil {
				errs = append(errs, fmt.Errorf("%s:%d:%d: %s",
					e.Location.File, e.Location.Line, e.Location.Column, e.Text))
			} else {
				errs = append(errs, errors.New(e.Text))
			}
		}
		return nil, errors.Join(errs...)
	}

	if len(result.OutputFiles) == 0 {
		return nil, errors.New("esbuild produced no output")
	}

	return result.OutputFiles[0].Contents, nil
}

// MustBuildJS is like BuildJS but it log.Fatal's on error. Intended for
// server startup where a bundler failure should abort.
func MustBuildJS(srcDir string) []byte {
	js, err := BuildJS(srcDir)
	if err != nil {
		log.Fatalf("build: %v", err)
	}
	return js
}
