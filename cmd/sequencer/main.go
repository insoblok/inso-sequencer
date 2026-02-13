package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	fmt.Printf("InSo Sequencer %s\n", version)
	fmt.Println("Starting InSoBlok L2 Sequencer...")

	// TODO: Initialize config
	// TODO: Initialize EVM execution engine
	// TODO: Initialize mempool with TasteScore fair ordering
	// TODO: Initialize block producer
	// TODO: Initialize L1 batch submitter
	// TODO: Start JSON-RPC server
	// TODO: Start WebSocket server

	fmt.Println("Sequencer is not yet implemented. See README.md for architecture.")
	os.Exit(0)
}
