// Command lana adalah binary tunggal lana-harness.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version diisi lewat -X main.version saat build release.
var version = "dev"

func main() {
	showVersion := flag.Bool("V", false, "cetak versi")
	flag.BoolVar(showVersion, "version", false, "cetak versi")
	flag.Parse()
	if *showVersion {
		fmt.Fprintf(os.Stdout, "lana %s\n", version)
		return
	}
	flag.Usage()
}
