//go:build !windows

package main

// Developer CLI for Linux/macOS. Patches a bin using a .ecu file you already
// generated with ME7Info. Does NOT correct checksums - run me7sum afterwards.
//
//   go build -o patchcli . && ./patchcli in.bin in.ecu out.bin

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: patchcli <in.bin> <in.ecu> <out.bin>")
		os.Exit(2)
	}
	bin, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ecu, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, r, err := Patch(bin, ParseECU(ecu))
	if r != nil {
		for _, l := range r.Log {
			fmt.Println("  " + l)
		}
	}
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[3], out, 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("written (checksums NOT corrected):", os.Args[3])
}
