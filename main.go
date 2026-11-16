package main

import (
	"fmt"
	"os"

	"github.com/hexadecimaltime/pingo-tui/app"
	"github.com/hexadecimaltime/pingo-tui/exports"
)

func main() {
	if err := app.Start(os.Args[1:], exports.Symbols); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}
