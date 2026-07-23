package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

func main() {
	// Headless codegen: `athene gen <form.json> <outdir>` generates and
	// compiles the app project without opening the GUI. Handy for CI/testing.
	if len(os.Args) >= 2 && os.Args[1] == "gen" {
		os.Exit(runGen(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == "lsp-test" {
		os.Exit(runLSPTest(os.Args[2:]))
	}

	gtkApp := gtk.NewApplication("com.athene.ide", 0)
	app := NewApp(gtkApp)
	gtkApp.ConnectActivate(func() { app.build() })
	os.Exit(gtkApp.Run(os.Args))
}

func runGen(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: athene gen <form.json> <outdir>")
		return 2
	}
	form, err := LoadForm(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "load form:", err)
		return 1
	}
	if err := writeProject(args[1], form); err != nil {
		fmt.Fprintln(os.Stderr, "codegen:", err)
		return 1
	}
	out, err := buildApp(args[1])
	fmt.Print(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build failed:", err)
		return 1
	}
	fmt.Println("OK: built", args[1]+"/app")
	return 0
}

// runLSPTest exercises the gopls client headlessly:
//   athene lsp-test <projectdir> <line> <char>
// It opens the project's handlers.go and prints completions at line/char
// (zero-based). Handy for validating the LSP layer without any GUI.
func runLSPTest(args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: athene lsp-test <projectdir> <line> <char>")
		return 2
	}
	dir := args[0]
	line, _ := strconv.Atoi(args[1])
	char, _ := strconv.Atoi(args[2])
	hp := handlersPath(dir)
	text, err := os.ReadFile(hp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read handlers:", err)
		return 1
	}
	client, err := StartGopls(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start gopls:", err)
		return 1
	}
	defer client.Close()
	if err := client.DidOpen(hp, string(text)); err != nil {
		fmt.Fprintln(os.Stderr, "didOpen:", err)
		return 1
	}
	items, err := client.Complete(line, char)
	if err != nil {
		fmt.Fprintln(os.Stderr, "complete:", err)
		return 1
	}
	fmt.Printf("%d completions at %d:%d\n", len(items), line, char)
	for i, it := range items {
		if i >= 25 {
			fmt.Println("  …")
			break
		}
		fmt.Printf("  %-24s %-8s %s\n", it.Label, it.kindName(), it.Detail)
	}
	return 0
}
