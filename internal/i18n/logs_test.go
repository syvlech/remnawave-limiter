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
			t.Errorf("%s: got %q, want %q", tc.lang, got, tc.want)
		}
		got := fmt.Sprintf(T("log.active_window_short"), 60, 120)
		if strings.Contains(got, "%!") || !strings.Contains(got, "60") || !strings.Contains(got, "120") {
			t.Errorf("%s: invalid formatted log: %s", tc.lang, got)
		}
	}
}

func TestRuntimeLogFormatTokensMatch(t *testing.T) {
	format := regexp.MustCompile(`%(?:\[[0-9]+\])?[-+#0 ]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)
	for key, en := range translations["en"] {
		if !strings.HasPrefix(key, "log.") {
			continue
		}
		want := strings.Join(format.FindAllString(en, -1), "|")
		for lang, locale := range translations {
			got := strings.Join(format.FindAllString(locale[key], -1), "|")
			if got != want {
				t.Errorf("%s %s: format tokens %q, want %q", lang, key, got, want)
			}
		}
	}
}

// Application log messages must use translation keys. Dynamic third-party
// diagnostics and structured fields intentionally retain their original values.
func TestNoHardcodedApplicationLogMessages(t *testing.T) {
	root := filepath.Join("..", "..")
	methods := map[string]bool{
		"Trace": true, "Tracef": true, "Traceln": true,
		"Debug": true, "Debugf": true, "Debugln": true,
		"Info": true, "Infof": true, "Infoln": true,
		"Warn": true, "Warnf": true, "Warnln": true,
		"Error": true, "Errorf": true, "Errorln": true,
		"Fatal": true, "Fatalf": true, "Fatalln": true,
		"Panic": true, "Panicf": true, "Panicln": true,
	}
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
						t.Errorf("%s: hardcoded application log message: %s", fset.Position(literal.Pos()), literal.Value)
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
