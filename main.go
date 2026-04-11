// Command test-server is a trivial static file server for local web
// development. It serves the current working directory over
// http://127.0.0.1:<port>, auto-reloads the browser when files change on
// disk, and sets the headers needed for cross-origin isolation (so Chrome
// unlocks high-resolution timers and SharedArrayBuffer).
package main

import "os"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
