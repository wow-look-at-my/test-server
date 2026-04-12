package main

import selfupdate "github.com/wow-look-at-my/go-selfupdate-mini"

func init() {
	repo := selfupdate.ParseSlug("wow-look-at-my/test-server")
	rootCmd.AddCommand(selfupdate.NewUpdateCommand(repo, version))
	rootCmd.AddCommand(selfupdate.NewInstallCommand(repo))
}
