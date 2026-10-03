package graph

import (
	"context"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type GoExtract struct {
	mu   sync.Mutex
	ext  types.Importer
	imps map[string]*modImporter
}

func (e *GoExtract) Extract(ctx context.Context, abs string) (Result, error) {
	if Skip(abs) {
		return Result{}, fmt.Errorf("graph: %s is skipped (vendor, testdata, or a dot or underscore path)", abs)
	}
	root, module, err := ProjectOf(abs)
	if err != nil {
		return Result{}, fmt.Errorf("graph: project of %s: %w", abs, err)
	}
	dir := filepath.Dir(abs)
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, abs, nil, 0)
	if err != nil {
		return Result{}, fmt.Errorf("graph: parse %s: %w", abs, err)
	}
	pkgs, _ := parser.ParseDir(fset, dir, nil, 0)
	pkg, ok := pkgs[src.Name.Name]
	if !ok {
		return Result{}, fmt.Errorf("graph: %s is package %s, absent from %s", abs, src.Name.Name, dir)
	}
	pkgPath := PackagePath(root, module, dir)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return Result{}, fmt.Errorf("graph: rel of %s: %w", abs, err)
	}
	fileRel := filepath.ToSlash(rel)

	var files []*ast.File
	var touched *ast.File
	for name, f := range pkg.Files {
		if filepath.Clean(name) == filepath.Clean(abs) {
			touched = f
		}
		files = append(files, f)
	}
	if touched == nil {
		return Result{}, fmt.Errorf("graph: %s is not among %s's parsed files", abs, dir)
	}
	sort.Slice(files, func(i, j int) bool {
		return fset.Position(files[i].Pos()).Filename < fset.Position(files[j].Pos()).Filename
	})
	names := map[string]bool{}
	for _, f := range files {
		for _, n := range declNames(f) {
			names[n] = true
		}
	}

	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	info := &types.Info{
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := &types.Config{Importer: e.importer(root, module), Error: func(error) {}}
	_, _ = conf.Check(pkgPath, fset, files, info)

	res := Result{Eager: true}
	res.Symbols = symbolsOf(fset, touched, pkgPath, fileRel, info)
	res.Edges = edgesOf(fset, touched, pkgPath, module, fileRel, info, names)
	sort.Slice(res.Edges, func(i, j int) bool {
		a, b := res.Edges[i], res.Edges[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.ToPackage != b.ToPackage {
			return a.ToPackage < b.ToPackage
		}
		return a.ToName < b.ToName
	})
	return res, nil
}

func (e *GoExtract) importer(root, module string) types.Importer {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ext == nil {
		e.ext = importer.Default()
	}
	if e.imps == nil {
		e.imps = map[string]*modImporter{}
	}
	m, ok := e.imps[root]
	if !ok {
		m = &modImporter{root: root, module: module, ext: e.ext, fset: token.NewFileSet(), cache: map[string]*types.Package{}}
		e.imps[root] = m
	}
	return m
}

type modImporter struct {
	root, module string
	ext          types.Importer
	fset         *token.FileSet
	mu           sync.Mutex
	cache        map[string]*types.Package
	pending      map[string]bool
}

func (m *modImporter) Import(path string) (*types.Package, error) {
	if path == m.module || strings.HasPrefix(path, m.module+"/") {
		rel := strings.TrimPrefix(path, m.module)
		return m.load(filepath.Join(m.root, filepath.FromSlash(rel)), path)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ext.Import(path)
}

func (m *modImporter) load(dir, path string) (*types.Package, error) {
	m.mu.Lock()
	if p, ok := m.cache[path]; ok {
		m.mu.Unlock()
		return p, nil
	}
	if m.pending == nil {
		m.pending = map[string]bool{}
	}
	if m.pending[path] {
		m.mu.Unlock()
		return nil, fmt.Errorf("graph: import cycle through %s", path)
	}
	m.pending[path] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.pending, path)
		m.mu.Unlock()
	}()
	pkgs, _ := parser.ParseDir(m.fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	for _, pkg := range pkgs {
		var files []*ast.File
		for _, f := range pkg.Files {
			files = append(files, f)
		}
		conf := &types.Config{Importer: m, Error: func(error) {}}
		p, _ := conf.Check(path, m.fset, files, &types.Info{})
		if p == nil {
			return nil, fmt.Errorf("graph: %s did not check", path)
		}
		m.mu.Lock()
		m.cache[path] = p
		m.mu.Unlock()
		return p, nil
	}
	return nil, fmt.Errorf("graph: %s holds no package", dir)
}

func declNames(file *ast.File) []string {
	var out []string
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			out = append(out, d.Name.Name)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						out = append(out, n.Name)
					}
				case *ast.TypeSpec:
					out = append(out, spec.Name.Name)
				}
			}
		}
	}
	return out
}

func symbolsOf(fset *token.FileSet, file *ast.File, pkgPath, fileRel string, info *types.Info) []Symbol {
	var out []Symbol
	add := func(name *ast.Ident, kind string) {
		out = append(out, Symbol{
			Package: pkgPath,
			Name:    name.Name,
			Kind:    kind,
			File:    fileRel,
			Line:    int64(fset.Position(name.Pos()).Line),
		})
	}
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				add(d.Name, KindFunc)
				continue
			}
			base := astRecvBase(d.Recv)
			if base == "" {
				if o, ok := info.Defs[d.Name]; ok {
					if fn, ok := o.(*types.Func); ok {
						if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
							base = typesRecvBase(sig)
						}
					}
				}
			}
			if base == "" {
				continue
			}
			out = append(out, Symbol{
				Package: pkgPath,
				Name:    base + "." + d.Name.Name,
				Kind:    KindMethod,
				File:    fileRel,
				Line:    int64(fset.Position(d.Name.Pos()).Line),
			})
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					kind := KindVar
					if d.Tok == token.CONST {
						kind = KindConst
					}
					for _, n := range spec.Names {
						add(n, kind)
					}
				case *ast.TypeSpec:
					add(spec.Name, KindType)
				}
			}
		}
	}
	return out
}

func astRecvBase(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	for {
		switch v := t.(type) {
		case *ast.StarExpr:
			t = v.X
		case *ast.IndexExpr:
			t = v.X
		case *ast.IndexListExpr:
			t = v.X
		case *ast.Ident:
			return v.Name
		default:
			return ""
		}
	}
}

func typesRecvBase(sig *types.Signature) string {
	t := sig.Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

func objKey(o types.Object) string {
	if fn, ok := o.(*types.Func); ok {
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			if base := typesRecvBase(sig); base != "" {
				return base + "." + o.Name()
			}
		}
	}
	return o.Name()
}

type ownerRange struct {
	start, end token.Pos
	name       string
}

func ownerList(file *ast.File) []ownerRange {
	var out []ownerRange
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			if d.Recv != nil {
				if base := astRecvBase(d.Recv); base != "" {
					name = base + "." + d.Name.Name
				}
			}
			out = append(out, ownerRange{d.Pos(), d.End(), name})
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						out = append(out, ownerRange{spec.Pos(), spec.End(), n.Name})
					}
				case *ast.TypeSpec:
					out = append(out, ownerRange{spec.Pos(), spec.End(), spec.Name.Name})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func ownerOf(owners []ownerRange, pos token.Pos) string {
	i := sort.Search(len(owners), func(i int) bool { return owners[i].start > pos }) - 1
	if i >= 0 && pos <= owners[i].end {
		return owners[i].name
	}
	return ""
}

func edgesOf(fset *token.FileSet, file *ast.File, pkgPath, module, fileRel string, info *types.Info, pkgNames map[string]bool) []Edge {
	owners := ownerList(file)
	var out []Edge
	emit := func(pos token.Pos, toPkg, toName string) {
		if toPkg == "" || toName == "" {
			return
		}
		if toPkg != module && !strings.HasPrefix(toPkg, module+"/") {
			return
		}
		from := ownerOf(owners, pos)
		if from == "" {
			return
		}
		out = append(out, Edge{
			FromPackage: pkgPath,
			FromName:    from,
			ToPackage:   toPkg,
			ToName:      toName,
			File:        fileRel,
			Line:        int64(fset.Position(pos).Line),
		})
	}
	imports := map[string]string{}
	for _, imp := range file.Imports {
		p := strings.Trim(imp.Path.Value, "\"`")
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "" {
			name = path.Base(p)
		}
		if name != "." && name != "_" {
			imports[name] = p
		}
	}
	selX, selSel := map[*ast.Ident]bool{}, map[*ast.Ident]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if s, ok := info.Selections[n]; ok {
				selSel[n.Sel] = true
				if fn, ok := s.Obj().(*types.Func); ok && fn.Pkg() != nil {
					if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
						if base := typesRecvBase(sig); base != "" {
							emit(n.Sel.Pos(), fn.Pkg().Path(), base+"."+fn.Name())
						}
					}
				}
			} else if _, used := info.Uses[n.Sel]; !used {
				selSel[n.Sel] = true
				if id, ok := n.X.(*ast.Ident); ok {
					if _, xused := info.Uses[id]; !xused {
						selX[id] = true
						if p, ok := imports[id.Name]; ok {
							emit(n.Sel.Pos(), p, n.Sel.Name)
						}
					}
				}
			}
		case *ast.Ident:
			if selX[n] || selSel[n] {
				return true
			}
			if o, ok := info.Uses[n]; ok {
				if _, isPkg := o.(*types.PkgName); !isPkg && o.Pkg() != nil && o.Parent() == o.Pkg().Scope() {
					emit(n.Pos(), o.Pkg().Path(), objKey(o))
				}
				return true
			}
			if _, def := info.Defs[n]; def {
				return true
			}
			if pkgNames[n.Name] {
				emit(n.Pos(), pkgPath, n.Name)
			}
		}
		return true
	})
	return out
}
