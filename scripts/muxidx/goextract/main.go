package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Chunk struct {
	ID        string `json:"id"`
	FilePath  string `json:"file_path"`
	ChunkType string `json:"chunk_type"`
	Name      string `json:"name"`
	Package   string `json:"package"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
}

type Edge struct {
	Source   string  `json:"source"`
	Target   string  `json:"target"`
	Relation string  `json:"relation"`
	Weight   float64 `json:"weight"`
	Metadata string  `json:"metadata,omitempty"`
}

type Output struct {
	Chunks []Chunk `json:"chunks"`
	Edges  []Edge  `json:"edges"`
}

type posRange struct{ start, end int }
type extractor struct {
	fset    *token.FileSet
	file    *ast.File
	path    string
	pkgName string
	chunks  []Chunk
	edges   []Edge
	imports map[string]string
	types   map[string]posRange
	funcs   map[string]posRange
}

func extractChunkID(path, pkg, name, kind string, start, end int) string {
	s := fmt.Sprintf("%s:%s:%s:%s:%d:%d", path, pkg, name, kind, start, end)
	return fmt.Sprintf("%x", []byte(s))
}

func (e *extractor) addChunk(typ, name, content string, start, end int) {
	id := extractChunkID(e.path, e.pkgName, name, typ, start, end)
	e.chunks = append(e.chunks, Chunk{ID: id, FilePath: e.path, ChunkType: typ, Name: name, Package: e.pkgName, StartLine: start, EndLine: end, Content: content})
}

func (e *extractor) getLine(pos token.Pos) int { return e.fset.Position(pos).Line }

func (e *extractor) extractImports() {
	for _, imp := range e.file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		var name string
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			parts := strings.Split(path, "/")
			name = parts[len(parts)-1]
		}
		e.imports[name] = path
	}
}

func sourceText(fset *token.FileSet, f *ast.File, start, end token.Pos) string {
	if !start.IsValid() || !end.IsValid() {
		return ""
	}
	startPos := fset.Position(start).Offset
	endPos := fset.Position(end).Offset
	content, err := os.ReadFile(fset.Position(f.Package).Filename)
	if err != nil {
		return ""
	}
	if startPos < 0 || endPos > len(content) || startPos > endPos {
		return ""
	}
	return string(content[startPos:endPos])
}

func (e *extractor) findTypeID(name string) (string, bool) {
	if r, ok := e.types[name]; ok {
		return extractChunkID(e.path, e.pkgName, name, "type", r.start, r.end), true
	}
	return "", false
}

func (e *extractor) visit(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.FuncDecl:
		name := v.Name.Name
		start := e.getLine(v.Pos())
		end := e.getLine(v.End())
		content := sourceText(e.fset, e.file, v.Pos(), v.End())
		kind := "function"
		if v.Recv != nil {
			kind = "method"
			recvType := receiverType(v.Recv)
			name = fmt.Sprintf("(%s).%s", recvType, name)
		}
		e.addChunk(kind, name, content, start, end)
		e.funcs[name] = posRange{start: start, end: end}
		containsID := extractChunkID(e.path, e.pkgName, e.path, "file", 0, 0)
		chunkID := extractChunkID(e.path, e.pkgName, name, kind, start, end)
		e.edges = append(e.edges, Edge{Source: containsID, Target: chunkID, Relation: "contains", Weight: 1})
		if kind == "method" {
			recvType := receiverType(v.Recv)
			if typeRange, ok := e.types[recvType]; ok {
				e.edges = append(e.edges, Edge{Source: chunkID, Target: extractChunkID(e.path, e.pkgName, recvType, "type", typeRange.start, typeRange.end), Relation: "receives", Weight: 1})
			}
		}
		for _, stmt := range v.Body.List {
			e.extractCalls(stmt, chunkID)
		}
	case *ast.GenDecl:
		for _, spec := range v.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				name := s.Name.Name
				start := e.getLine(s.Pos())
				end := e.getLine(s.End())
				content := sourceText(e.fset, e.file, s.Pos(), s.End())
				e.addChunk("type", name, content, start, end)
				e.types[name] = posRange{start: start, end: end}
				containsID := extractChunkID(e.path, e.pkgName, e.path, "file", 0, 0)
				typeID := extractChunkID(e.path, e.pkgName, name, "type", start, end)
				e.edges = append(e.edges, Edge{Source: containsID, Target: typeID, Relation: "defines_type", Weight: 1})
				if t, ok := s.Type.(*ast.StructType); ok {
					e.extractStructEmbeddings(name, typeID, t)
				}
				if t, ok := s.Type.(*ast.InterfaceType); ok {
					e.extractInterfaceRefs(name, typeID, t)
				}
			case *ast.ValueSpec:
				for _, name := range s.Names {
					if name.IsExported() {
						start := e.getLine(s.Pos())
						end := e.getLine(s.End())
						content := sourceText(e.fset, e.file, s.Pos(), s.End())
						e.addChunk("variable", name.Name, content, start, end)
						containsID := extractChunkID(e.path, e.pkgName, e.path, "file", 0, 0)
						varID := extractChunkID(e.path, e.pkgName, name.Name, "variable", start, end)
						e.edges = append(e.edges, Edge{Source: containsID, Target: varID, Relation: "contains", Weight: 1})
					}
				}
			}
		}
	}
	return true
}

func (e *extractor) extractStructEmbeddings(structName, structID string, t *ast.StructType) {
	for _, field := range t.Fields.List {
		typ := typeExprToString(field.Type)
		if targetID, ok := e.findTypeID(typ); ok {
			if len(field.Names) == 0 {
				e.edges = append(e.edges, Edge{Source: structID, Target: targetID, Relation: "embeds", Weight: 1})
			} else {
				for _, name := range field.Names {
					e.edges = append(e.edges, Edge{Source: structID, Target: targetID, Relation: "composes", Weight: 1, Metadata: fmt.Sprintf(`{"field":"%s"}`, name.Name)})
				}
			}
		}
	}
}

func (e *extractor) extractInterfaceRefs(ifaceName, ifaceID string, t *ast.InterfaceType) {
	for _, method := range t.Methods.List {
		if len(method.Names) == 0 {
			if sel, ok := method.Type.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok {
					if targetID, ok := e.findTypeID(sel.Sel.Name); ok {
						e.edges = append(e.edges, Edge{Source: ifaceID, Target: targetID, Relation: "extends", Weight: 1, Metadata: fmt.Sprintf(`{"qualified":"%s.%s"}`, pkg.Name, sel.Sel.Name)})
					}
				}
			}
		}
	}
}

func (e *extractor) extractCalls(n ast.Node, sourceID string) {
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		var targetName string
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			targetName = fun.Name
		case *ast.SelectorExpr:
			if x, ok := fun.X.(*ast.Ident); ok {
				targetName = fmt.Sprintf("%s.%s", x.Name, fun.Sel.Name)
			} else {
				targetName = fun.Sel.Name
			}
		default:
			return true
		}
		if builtins[targetName] {
			return true
		}
		for fnName := range e.funcs {
			if strings.HasSuffix(fnName, "."+targetName) || fnName == targetName {
				fnRange := e.funcs[fnName]
				targetID := extractChunkID(e.path, e.pkgName, fnName, "function", fnRange.start, fnRange.end)
				e.edges = append(e.edges, Edge{Source: sourceID, Target: targetID, Relation: "calls", Weight: 1})
				break
			}
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				if pkgPath, isImport := e.imports[pkgIdent.Name]; isImport {
					e.edges = append(e.edges, Edge{Source: sourceID, Target: pkgPath, Relation: "references", Weight: 0.8, Metadata: fmt.Sprintf(`{"qualified":"%s.%s","import_path":"%s"}`, pkgIdent.Name, sel.Sel.Name, pkgPath)})
				}
			}
		}
		return true
	})
}

func receiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return typeExprToString(recv.List[0].Type)
}

func typeExprToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeExprToString(t.X)
	case *ast.SelectorExpr:
		return typeExprToString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeExprToString(t.Elt)
	case *ast.MapType:
		return "map[" + typeExprToString(t.Key) + "]" + typeExprToString(t.Value)
	case *ast.IndexExpr:
		return typeExprToString(t.X) + "[" + typeExprToString(t.Index) + "]"
	case *ast.IndexListExpr:
		return typeExprToString(t.X) + "[" + typeExprToString(t.Indices[0]) + "]"
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return fmt.Sprintf("%T", t)
	}
}

var builtins = map[string]bool{
	"append": true, "cap": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true,
	"make": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: goextract <file.go> [file.go ...]")
		os.Exit(1)
	}
	var all Output
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", arg, err)
			continue
		}
		if info.IsDir() || !strings.HasSuffix(arg, ".go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, arg, nil, parser.ParseComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", arg, err)
			continue
		}
		if f.Name == nil {
			continue
		}
		absPath, _ := filepath.Abs(arg)
		e := &extractor{
			fset: fset, file: f, path: absPath, pkgName: f.Name.Name,
			imports: make(map[string]string), types: make(map[string]posRange), funcs: make(map[string]posRange),
		}
		e.addChunk("file", e.path, sourceText(fset, f, f.Pos(), f.End()), 0, 0)
		fileID := extractChunkID(absPath, e.pkgName, absPath, "file", 0, 0)
		pkgID := extractChunkID(absPath, e.pkgName, e.pkgName, "package", 0, 0)
		e.edges = append(e.edges, Edge{Source: fileID, Target: pkgID, Relation: "belongs_to", Weight: 1})
		e.extractImports()
		for _, impName := range sortedKeys(e.imports) {
			impPath := e.imports[impName]
			e.edges = append(e.edges, Edge{Source: fileID, Target: impPath, Relation: "imports", Weight: 1, Metadata: fmt.Sprintf(`{"name":"%s","path":"%s"}`, impName, impPath)})
			e.edges = append(e.edges, Edge{Source: pkgID, Target: impPath, Relation: "imports", Weight: 1, Metadata: fmt.Sprintf(`{"name":"%s","path":"%s"}`, impName, impPath)})
		}
		ast.Walk(e, f)
		all.Chunks = append(all.Chunks, e.chunks...)
		all.Edges = append(all.Edges, e.edges...)
	}
	out, _ := json.MarshalIndent(all, "", "  ")
	fmt.Println(string(out))
}

func sortedKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
