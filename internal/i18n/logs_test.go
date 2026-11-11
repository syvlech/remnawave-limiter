package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeLogTranslations(t *testing.T) {
	t.Cleanup(func() { SetLanguage("ru") })
	for _, tc := range []struct {
		lang string
		want string
	}{
		{"ru", "Redis подключён"},
		{"en", "Redis connected"},
		{"zh", "Redis 已连接"},
	} {
		SetLanguage(tc.lang)
		if got := T("log.redis_connected"); got != tc.want {
			t.Errorf("%s: получено %q, ожидалось %q", tc.lang, got, tc.want)
		}
		got := fmt.Sprintf(T("log.active_window_short"), 60, 120)
		if strings.Contains(got, "%!") || !strings.Contains(got, "60") || !strings.Contains(got, "120") {
			t.Errorf("%s: лог отформатирован неверно: %s", tc.lang, got)
		}
	}
}

func forEachSourceFile(t *testing.T, fn func(path string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			fn(path, fset, file)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoHardcodedApplicationLogMessages(t *testing.T) {
	methods := map[string]bool{
		"Trace": true, "Tracef": true, "Traceln": true,
		"Debug": true, "Debugf": true, "Debugln": true,
		"Info": true, "Infof": true, "Infoln": true,
		"Warn": true, "Warnf": true, "Warnln": true,
		"Error": true, "Errorf": true, "Errorln": true,
		"Fatal": true, "Fatalf": true, "Fatalln": true,
		"Panic": true, "Panicf": true, "Panicln": true,
	}
	forEachSourceFile(t, func(_ string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !methods[sel.Sel.Name] {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "fmt" || id.Name == "bl") {
				return true
			}
			if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
				text, err := strconv.Unquote(literal.Value)
				if err != nil || text != "" {
					t.Errorf("%s: текст лога задан строкой, а не через i18n: %s", fset.Position(literal.Pos()), literal.Value)
				}
			}
			return true
		})
	})
}

func TestNoCyrillicStringLiterals(t *testing.T) {
	cyrillic := regexp.MustCompile(`\p{Cyrillic}`)
	i18nDir := filepath.Join("..", "..", "internal", "i18n")
	forEachSourceFile(t, func(path string, fset *token.FileSet, file *ast.File) {
		if filepath.Dir(path) == i18nDir {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			literal, ok := n.(*ast.BasicLit)
			if ok && literal.Kind == token.STRING && cyrillic.MatchString(literal.Value) {
				t.Errorf("%s: русский текст в коде, вынесите его в i18n: %s", fset.Position(literal.Pos()), literal.Value)
			}
			return true
		})
	})
}
