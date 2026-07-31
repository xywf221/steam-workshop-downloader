// Package cli defines the Kong command tree for the swd binary.
package cli

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"

	"github.com/xywf221/steam-workshop-downloader/internal/ui"
	"github.com/xywf221/steam-workshop-downloader/internal/version"
)

// CLI is the root Kong grammar.
type CLI struct {
	Download DownloadCmd `cmd:"" help:"Download Steam Workshop items"`
	Web      WebCmd      `cmd:"" help:"Workshop browse / download web UI (phase 2)"`
	Version  VersionCmd  `cmd:"" help:"Print version and exit"`
}

// VersionCmd prints the app version.
type VersionCmd struct{}

// Run implements kong Runner.
func (c *VersionCmd) Run() error {
	fmt.Printf("%s %s\n", version.AppName, version.Version)
	return nil
}

// WebCmd is a phase-2 stub.
type WebCmd struct {
	Host   string `default:"127.0.0.1" help:"Bind host"`
	Port   int    `default:"8765" help:"Bind port"`
	Output string `short:"o" default:"." help:"Download directory"`
	Proxy  string `help:"Optional proxy URL (socks5/socks4/http)"`
}

// Run implements kong Runner.
func (c *WebCmd) Run() error {
	return fmt.Errorf("swd web is not implemented yet (phase 2)")
}

// Run parses argv and executes the selected subcommand.
// Returns a process exit code.
func Run(args []string) int {
	ui.EnableVT()

	// Transitional: `swd 294100 123` → insert "download".
	args = maybeInsertDownload(args)

	var root CLI
	parser, err := kong.New(&root,
		kong.Name("swd"),
		kong.Description(version.AppName+" — Go rewrite (no SteamCMD, first-party Steam protocol)"),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
		kong.Vars{"version": version.Version},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "swd: internal CLI error: %v\n", err)
		return 1
	}

	kongCtx, err := parser.Parse(args)
	if err != nil {
		parser.FatalIfErrorf(err)
		// FatalIfErrorf calls exit; if tests intercept, still return 2.
		return 2
	}
	if err := kongCtx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL %v\n", err)
		return 1
	}
	return 0
}

// maybeInsertDownload rewrites legacy positional form:
//
//	swd <appid> <workshopid...>  →  swd download <appid> <workshopid...>
//
// when the first argument looks like a positive integer and is not a flag/subcommand.
func maybeInsertDownload(args []string) []string {
	if len(args) == 0 {
		return args
	}
	first := args[0]
	if first == "download" || first == "web" || first == "version" || first == "help" {
		return args
	}
	if len(first) > 0 && first[0] == '-' {
		return args
	}
	// All digits?
	for _, r := range first {
		if r < '0' || r > '9' {
			return args
		}
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, "download")
	out = append(out, args...)
	return out
}
