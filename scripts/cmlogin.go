//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/xywf221/steam-workshop-downloader/internal/proxy"
	"github.com/xywf221/steam-workshop-downloader/internal/steam/cm"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var client *cm.Client
	var err error
	if len(os.Args) > 1 {
		p, err2 := proxy.Parse(os.Args[1])
		if err2 != nil {
			panic(err2)
		}
		fmt.Println("proxy", p.RedactedURL())
		client, err = cm.NewWithProxy(&p)
	} else {
		fmt.Println("direct")
		client = cm.New(nil, nil)
	}
	if err != nil {
		panic(err)
	}
	client.Logf = func(f string, a ...any) { fmt.Printf("CM: "+f+"\n", a...) }
	err = client.ConnectAndLogin(ctx, 5)
	if err != nil {
		fmt.Println("FAIL", err)
		os.Exit(1)
	}
	fmt.Println("OK steamid", client.SteamID(), "cell", client.CellID(), "logged", client.LoggedOn())
	client.Disconnect()
}
