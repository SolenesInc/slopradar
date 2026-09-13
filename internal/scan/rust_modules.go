package scan

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

type moduleContext struct {
	file      int
	directory string
}

type moduleEdge struct {
	to        moduleContext
	testOnly  bool
	uncertain bool
}

type moduleResolver struct {
	files    []analyzedFile
	index    map[string]int
	edges    map[moduleContext][]moduleEdge
	incoming map[int]bool
	warnings map[moduleContext][]string
	pending  []moduleContext
	packages map[string]bool
	ignored  map[string]bool
	config   model.Config
}

func classifyRustModules(files []analyzedFile, ignored, packages map[string]bool, config model.Config) []string {
	r := moduleResolver{
		files: files, ignored: ignored, packages: packages, config: config,
		index: map[string]int{}, edges: map[moduleContext][]moduleEdge{},
		incoming: map[int]bool{}, warnings: map[moduleContext][]string{},
	}
	for i, file := range files {
		if file.analysis.language == "rust" {
			r.index[file.blob.Path] = i
			r.pending = append(r.pending, r.defaultContext(i))
		}
	}
	r.discover()
	return r.classify()
}

func (r *moduleResolver) defaultContext(file int) moduleContext {
	name := r.files[file].blob.Path
	return moduleContext{file, rustModuleDirectory(name, r.crateRoot(name))}
}

func rustModuleDirectory(file string, explicit bool) string {
	directory := path.Dir(file)
	if !explicit && path.Base(file) != "mod.rs" {
		directory = path.Join(directory, strings.TrimSuffix(path.Base(file), path.Ext(file)))
	}
	return directory
}

func (r *moduleResolver) discover() {
	seen := map[moduleContext]bool{}
	for len(r.pending) > 0 {
		current := r.pending[len(r.pending)-1]
		r.pending = r.pending[:len(r.pending)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		file := r.files[current.file]
		r.resolve(current, file.result.Modules, current.directory, path.Dir(file.blob.Path), false, false)
	}
}

func (r *moduleResolver) classify() []string {
	type state struct {
		context moduleContext
		bucket  model.Bucket
	}
	var pending []state
	for i, file := range r.files {
		if file.analysis.language == "rust" && (!r.incoming[i] || r.crateRoot(file.blob.Path)) {
			pending = append(pending, state{r.defaultContext(i), file.bucket})
		}
	}
	seen := map[state]bool{}
	sourceFiles, testFiles := map[int]bool{}, map[int]bool{}
	warnings := map[string]bool{}
	propagate := func() {
		for len(pending) > 0 {
			current := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			file := r.files[current.context.file]
			if file.bucket == model.Tests || file.result.TestOnly {
				current.bucket = model.Tests
			}
			if seen[current] {
				continue
			}
			seen[current] = true
			if current.bucket == model.Tests {
				testFiles[current.context.file] = true
			} else {
				sourceFiles[current.context.file] = true
			}
			for _, warning := range r.warnings[current.context] {
				warnings[warning] = true
			}
			for _, edge := range r.edges[current.context] {
				bucket := current.bucket
				if edge.testOnly {
					bucket = model.Tests
				}
				if edge.uncertain {
					bucket = model.Source
				}
				pending = append(pending, state{edge.to, bucket})
			}
		}
	}
	propagate()
	for i, file := range r.files {
		if file.analysis.language == "rust" && !sourceFiles[i] && !testFiles[i] {
			pending = append(pending, state{r.defaultContext(i), model.Source})
		}
	}
	propagate()
	for i := range r.files {
		if testFiles[i] && !sourceFiles[i] {
			r.files[i].bucket = model.Tests
		}
	}
	result := make([]string, 0, len(warnings))
	for warning := range warnings {
		result = append(result, warning)
	}
	sort.Strings(result)
	return result
}

func (r *moduleResolver) resolve(from moduleContext, modules []lang.Module, moduleDir, attributeDir string, testOnly, uncertain bool) {
	for _, module := range modules {
		test := testOnly || module.TestOnly
		unknown := uncertain || module.Uncertain
		var paths []string
		if module.Path != nil {
			if target, ok := snapshotModulePath(attributeDir, *module.Path); ok {
				paths = append(paths, target)
			} else {
				unknown = true
				r.invalidPath(from, module.Name, *module.Path)
			}
		}
		for _, alternative := range module.AlternatePaths {
			if target, ok := snapshotModulePath(attributeDir, alternative); ok {
				paths = append(paths, target)
			} else {
				unknown = true
				r.invalidPath(from, module.Name, alternative)
			}
		}
		if module.Uncertain {
			r.warnings[from] = append(r.warnings[from], fmt.Sprintf("Rust module %s in %s: conditional or unsupported path; possible targets retain source scope", module.Name, r.files[from.file].blob.Path))
		}
		if module.Inline {
			if module.Path == nil {
				paths = append(paths, path.Join(moduleDir, module.Name))
			}
			for _, dir := range paths {
				r.resolve(from, module.Children, dir, dir, test, unknown)
			}
			continue
		}
		explicitPaths := len(paths)
		if module.Path == nil {
			paths = append(paths, path.Join(moduleDir, module.Name+".rs"), path.Join(moduleDir, module.Name, "mod.rs"))
		}
		var targets []moduleContext
		for i, file := range paths {
			if target, ok := r.index[file]; ok && !strings.HasPrefix(file, "../") && !path.IsAbs(file) {
				targets = append(targets, moduleContext{target, rustModuleDirectory(file, i < explicitPaths)})
			}
		}
		if len(targets) == 0 && !r.intentionallyOmitted(paths) {
			r.warnings[from] = append(r.warnings[from], fmt.Sprintf("Rust module %s in %s: no analyzed target among %q", module.Name, r.files[from.file].blob.Path, paths))
		}
		if len(targets) > 1 && !unknown {
			unknown = true
			r.warnings[from] = append(r.warnings[from], fmt.Sprintf("Rust module %s in %s: multiple analyzed targets among %q; retaining source scope", module.Name, r.files[from.file].blob.Path, paths))
		}
		for _, to := range targets {
			r.edges[from] = append(r.edges[from], moduleEdge{to: to, testOnly: test, uncertain: unknown})
			r.incoming[to.file] = true
			r.pending = append(r.pending, to)
		}
	}
}

func snapshotModulePath(base, name string) (string, bool) {
	if path.IsAbs(name) || strings.ContainsRune(name, 0) {
		return "", false
	}
	target := path.Join(base, name)
	return target, target != ".." && !strings.HasPrefix(target, "../")
}

func (r *moduleResolver) invalidPath(from moduleContext, module, name string) {
	r.warnings[from] = append(r.warnings[from], fmt.Sprintf("Rust module %s in %s: path %q is outside the analyzed snapshot or invalid", module, r.files[from.file].blob.Path, name))
}

func (r *moduleResolver) crateRoot(file string) bool {
	for directory := path.Dir(file); directory != "."; directory = path.Dir(directory) {
		if r.packages[directory] {
			return rustCrateRoot(strings.TrimPrefix(file, directory+"/"))
		}
	}
	return rustCrateRoot(file)
}

func rustCrateRoot(file string) bool {
	base, dir := path.Base(file), path.Dir(file)
	if rustTargetDirectory(dir) {
		return true
	}
	if file == "build.rs" {
		return true
	}
	if base != "main.rs" && base != "lib.rs" {
		return false
	}
	if dir == "." || dir == "src" {
		return true
	}
	return base == "main.rs" && rustTargetDirectory(path.Dir(dir))
}

func rustTargetDirectory(dir string) bool {
	switch dir {
	case "tests", "examples", "benches", "src/bin":
		return true
	default:
		return false
	}
}

func (r *moduleResolver) intentionallyOmitted(paths []string) bool {
	for _, file := range paths {
		if r.ignored[file] || model.Classify(file, nil, r.config).Excluded {
			return true
		}
	}
	return false
}
