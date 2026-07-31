// Command swd is the Steam Workshop Downloader CLI (Go rewrite).
package main

import (
	"os"

	"github.com/xywf221/steam-workshop-downloader/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
