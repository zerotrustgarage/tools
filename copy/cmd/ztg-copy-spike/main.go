// ztg-copy-spike captures RC0 evidence. It is not the copy application.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/zerotrustgarage/tools/copy/internal/spike"
)

func main() {
	output := flag.String("output", "", "new folder for synthetic Windows capture evidence")
	flag.Parse()
	if *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: ztg-copy-spike --output <new folder>")
		os.Exit(3)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := spike.Run(ctx, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	fmt.Println("RC0 captures written to", *output)
}
