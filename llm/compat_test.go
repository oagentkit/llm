package llm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	root "github.com/oagentkit/llm"
)

// 兼容层的**唯一**职责：让 github.com/oagentkit/llm/llm 继续可用。
// 因此「本包的导出符号集合 == 根包的导出符号集合」是它的核心不变量 ——
// 少一个，下游就有一个符号编译不过；多一个，就是本包自己长出了不该有的语义。
//
// 这条测试用 AST 比对而不是靠人记：根包每次增删导出符号，
// 忘记同步兼容层就会在这里红，而不是等到某个下游升级时才现场发现。

type exportSet struct {
	types  map[string]bool
	funcs  map[string]bool
	vars   map[string]bool
	consts map[string]bool
}

func (s exportSet) all() map[string][]string {
	out := map[string][]string{}
	for kind, m := range map[string]map[string]bool{
		"type": s.types, "func": s.funcs, "var": s.vars, "const": s.consts,
	} {
		for n := range m {
			out[kind] = append(out[kind], n)
		}
	}
	for _, v := range out {
		sort.Strings(v)
	}
	return out
}

func (s exportSet) names() map[string]bool {
	out := map[string]bool{}
	for _, m := range map[string]map[string]bool{
		"type": s.types, "func": s.funcs, "var": s.vars, "const": s.consts,
	} {
		for n := range m {
			out[n] = true
		}
	}
	return out
}

// parseExports 解析 dir 下的包，返回它的导出符号集合。
// 只看该目录**直接**包含的 .go 文件（不递归）——根包在 module 根，
// 本包在 llm/ 子目录，两者的文件都平铺在自己那一层。
func parseExports(t *testing.T, dir string, pkgName string) (exportSet, map[string]*ast.TypeSpec) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", dir, err)
	}
	pkg, ok := pkgs[pkgName]
	if !ok {
		names := make([]string, 0, len(pkgs))
		for n := range pkgs {
			names = append(names, n)
		}
		sort.Strings(names)
		t.Fatalf("%s 下找不到包 %q，实际有：%v", dir, pkgName, names)
	}

	out := exportSet{
		types: map[string]bool{}, funcs: map[string]bool{},
		vars: map[string]bool{}, consts: map[string]bool{},
	}
	typeSpecs := map[string]*ast.TypeSpec{}
	for _, f := range pkg.Files {
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				// 方法不算包级符号：方法跟着类型走，类型别名已经带过去了。
				if decl.Recv != nil || !decl.Name.IsExported() {
					continue
				}
				out.funcs[decl.Name.Name] = true
			case *ast.GenDecl:
				for _, s := range decl.Specs {
					switch spec := s.(type) {
					case *ast.TypeSpec:
						if !spec.Name.IsExported() {
							continue
						}
						out.types[spec.Name.Name] = true
						typeSpecs[spec.Name.Name] = spec
					case *ast.ValueSpec:
						for _, n := range spec.Names {
							if !n.IsExported() {
								continue
							}
							switch decl.Tok {
							case token.CONST:
								out.consts[n.Name] = true
							case token.VAR:
								out.vars[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return out, typeSpecs
}

func TestCompatParity(t *testing.T) {
	rootDir := ".." // module 根：根包在这里
	selfDir := "."  // 本包（llm/llm）

	rootExports, _ := parseExports(t, rootDir, "llm")
	selfExports, selfTypes := parseExports(t, selfDir, "llm")

	rootNames := rootExports.names()
	selfNames := selfExports.names()

	var missing, extra []string
	for n := range rootNames {
		if !selfNames[n] {
			missing = append(missing, n)
		}
	}
	for n := range selfNames {
		if !rootNames[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("兼容层缺少根包的导出符号（下游会编译不过）：%v\n"+
			"修法：在 compat.go 里补上对应的转发（类型用 type alias，函数用 var 转发）", missing)
	}
	if len(extra) > 0 {
		t.Errorf("兼容层多出根包没有的导出符号（兼容层不得自己长语义）：%v\n"+
			"修法：删掉 compat.go 里对应的行", extra)
	}

	// 分类明细：给出「少在哪一类」，避免只看到一个扁平的名字列表。
	rootByKind := map[string]map[string]bool{
		"type": rootExports.types, "func": rootExports.funcs,
		"var": rootExports.vars, "const": rootExports.consts,
	}
	selfByKind := map[string]map[string]bool{
		"type": selfExports.types, "func": selfExports.funcs,
		"var": selfExports.vars, "const": selfExports.consts,
	}
	for kind, rootMap := range rootByKind {
		selfMap := selfByKind[kind]
		var gap []string
		for n := range rootMap {
			if !selfMap[n] {
				gap = append(gap, n)
			}
		}
		if len(gap) > 0 {
			sort.Strings(gap)
			t.Logf("根包的 %s 未被镜像：%v", kind, gap)
		}
	}

	// 类型必须是**别名**（type X = root.X），不能是新定义。
	// 新定义会让下游的 `func(m model.BaseChatModel, ...) *llm.Client` 这类
	// 签名与根包不再同一类型——那就不是兼容层了。
	for name, spec := range selfTypes {
		if spec.Assign == token.NoPos {
			t.Errorf("兼容层的类型 %s 不是别名（写成了新定义）：必须是 `type %s = root.%s`", name, name, name)
		}
	}
}

// TestCompatNoWrapperFunc 守住「函数用 var 转发、不得写包装函数」。
//
// 包装函数要**逐个复制签名**：根包改了签名，包装函数不会编译错误，
// 而是变成另一个签名——兼容层从此与根包悄悄分叉，且不会有任何编译错误提示。
// var 转发把签名交给编译器守着：根包改了，这里跟着改。
func TestCompatNoWrapperFunc(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	pkg := pkgs["llm"]
	for _, f := range pkg.Files {
		for _, d := range f.Decls {
			decl, ok := d.(*ast.FuncDecl)
			if !ok || decl.Recv != nil {
				continue
			}
			if decl.Name.Name == "init" {
				continue
			}
			t.Errorf("兼容层不得声明函数 %s：包级函数一律用 var 转发根包"+
				"（包装函数会复制签名，根包改签名时这里静默分叉）", decl.Name.Name)
		}
	}
}

// TestCompatAliasIdentity 证明兼容层与根包是**同一个**类型 / 同一个函数值。
//
// 这是兼容层的语义底线：如果只是「结构相同的两份」，
// 下游拿兼容层的 Client 传给要求根包 Client 的接口就会编译不过。
func TestCompatAliasIdentity(t *testing.T) {
	// 类型：编译期即可判定（别名 ⇒ 同一类型 ⇒ 可直接赋值）。
	var (
		_ root.Client     = Client{}
		_ root.Generator  = Generator(nil)
		_ root.Budget     = Budget{}
		_ root.PriceTable = PriceTable{}
		_ root.Sink       = Sink(nil)
	)

	// 函数：运行时比对函数地址——必须是**同一个**函数值，不是转发壳。
	if got, want := reflect.ValueOf(NewClient).Pointer(), reflect.ValueOf(root.NewClient).Pointer(); got != want {
		t.Errorf("NewClient 不是根包同一个函数值：兼容层 %v ≠ 根包 %v", got, want)
	}
	if got, want := reflect.ValueOf(NewBudget).Pointer(), reflect.ValueOf(root.NewBudget).Pointer(); got != want {
		t.Errorf("NewBudget 不是根包同一个函数值：兼容层 %v ≠ 根包 %v", got, want)
	}

	// 变量：按值复制但共享底层数据（切片头指向同一块数组）。
	if got, want := reflect.ValueOf(BusyAdmissionDelays).Pointer(),
		reflect.ValueOf(root.BusyAdmissionDelays).Pointer(); got != want {
		t.Errorf("BusyAdmissionDelays 与根包不共享底层数组：%v ≠ %v", got, want)
	}
	if !reflect.DeepEqual(BusyAdmissionDelays, root.BusyAdmissionDelays) {
		t.Errorf("BusyAdmissionDelays 与根包取值不一致：%v ≠ %v",
			BusyAdmissionDelays, root.BusyAdmissionDelays)
	}
}

// TestCompatPackageIsImportable 证明这个包**真的**能被下游按老路径 import。
//
// import 路径 = module 路径 + 目录名 = github.com/oagentkit/llm/llm。
// 这个断言是给「目录放错位置」兜底的：目录名一变，老路径就又没了。
func TestCompatPackageIsImportable(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	if got, want := filepath.Base(wd), "llm"; got != want {
		t.Errorf("兼容层目录名是 %q，应为 %q：目录名决定 import 路径末段，"+
			"改了就等于把老路径又改没了", got, want)
	}
}
