package main

import (
	_ "github.com/vito/tuist/docs/go" // registers tuist + chroma plugins

	"github.com/vito/booklit/booklitcmd"
)

func main() {
	booklitcmd.Main()
}
