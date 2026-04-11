package main

import (
	"log"
	"os/exec"
	"runtime"
)

// browserCommand returns the argv used to open url in the user's default
// browser on the current OS.
func browserCommand(goos, url string) []string {
	switch goos {
	case "darwin":
		return []string{"open", url}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}
	default:
		return []string{"xdg-open", url}
	}
}

// browserStarter actually launches the subprocess. It's a package variable
// so tests can swap in a stub that records invocations without spawning
// anything real.
var browserStarter = func(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func openBrowser(url string) {
	argv := browserCommand(runtime.GOOS, url)
	if err := browserStarter(argv); err != nil {
		log.Printf("open browser: %v (set --no-open-browser to silence)", err)
	}
}
