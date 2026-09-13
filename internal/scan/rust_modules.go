package scan

import (
	"fmt"
	"path"
	"strings"

	"github.com/SolenesInc/slopradar/internal/lang"
	"github.com/SolenesInc/slopradar/internal/model"
)

type moduleEdge struct {
	to        int
	testOnly  bool
	uncertain bool
}

type moduleResolver struct {
	files    []analyzedFile
	index    map[string]int
	edges    map[int][]moduleEdge
	incoming map[int]bool
	warnings []string
}

func classifyRustModules(files []analyzedFile) []string {
	r := moduleResolver{files: files, index: map[string]int{}, edges: map[int][]moduleEdge{}, incoming: map[int]bool{}}
	for i, file := range files {
		if file.analysis.language == "rust" {
			r.index[file.blob.Path] = i
		}
	}
	for i, file := range files {
		if file.analysis.language != "rust" {
			continue
		}
		dir := path.Dir(file.blob.Path)
		moduleDir := dir
		switch path.Base(file.blob.Path) {
		case "lib.rs", "main.rs", "mod.rs":
		default:
			moduleDir = path.Join(dir, strings.TrimSuffix(path.Base(file.blob.Path), path.Ext(file.blob.Path)))
		}
		r.resolve(i, file.result.Modules, moduleDir, dir, false, false)
	}
	type state struct {
		file   int
		bucket model.Bucket
	}
	var pending []state
	for i, file := range files {
		if file.analysis.language != "rust" {
			continue
		}
		if !r.incoming[i] || file.bucket == model.Tests || file.result.TestOnly || path.Base(file.blob.Path) == "lib.rs" || path.Base(file.blob.Path) == "main.rs" {
			pending = append(pending, state{i, file.bucket})
		}
	}
	seen := map[state]bool{}
	propagate := func() {
		for len(pending) > 0 {
			current := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			file := files[current.file]
			if file.bucket == model.Tests || file.result.TestOnly {
				current.bucket = model.Tests
			}
			if seen[current] {
				continue
			}
			seen[current] = true
			for _, edge := range r.edges[current.file] {
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
	for i, file := range files {
		if file.analysis.language == "rust" && !seen[state{i, model.Source}] && !seen[state{i, model.Tests}] {
			pending = append(pending, state{i, model.Source})
		}
	}
	propagate()
	for i := range files {
		if seen[state{i, model.Tests}] && !seen[state{i, model.Source}] {
			files[i].bucket = model.Tests
		}
	}
	return r.warnings
}

func (r *moduleResolver) resolve(from int, modules []lang.Module, moduleDir, attributeDir string, testOnly, uncertain bool) {
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
			r.warnings = append(r.warnings, fmt.Sprintf("Rust module %s in %s: conditional or unsupported path; possible targets retain source scope", module.Name, r.files[from].blob.Path))
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
		if module.Path == nil {
			paths = append(paths, path.Join(moduleDir, module.Name+".rs"), path.Join(moduleDir, module.Name, "mod.rs"))
		}
		var targets []int
		for _, file := range paths {
			if target, ok := r.index[file]; ok && !strings.HasPrefix(file, "../") && !path.IsAbs(file) {
				targets = append(targets, target)
			}
		}
		if len(targets) == 0 {
			r.warnings = append(r.warnings, fmt.Sprintf("Rust module %s in %s: no analyzed target among %q", module.Name, r.files[from].blob.Path, paths))
		}
		if len(targets) > 1 && !unknown {
			unknown = true
			r.warnings = append(r.warnings, fmt.Sprintf("Rust module %s in %s: multiple analyzed targets among %q; retaining source scope", module.Name, r.files[from].blob.Path, paths))
		}
		for _, to := range targets {
			r.edges[from] = append(r.edges[from], moduleEdge{to: to, testOnly: test, uncertain: unknown})
			r.incoming[to] = true
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

func (r *moduleResolver) invalidPath(from int, module, name string) {
	r.warnings = append(r.warnings, fmt.Sprintf("Rust module %s in %s: path %q is outside the analyzed snapshot or invalid", module, r.files[from].blob.Path, name))
}
