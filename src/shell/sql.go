/**
Copyright (c) 2016 The ConnectorDB Contributors
Licensed under the MIT license.
**/
package shell

/* Provides the ability to open the sql database locally.

Copyright 2015 - The ConnectorDB Contributors; see AUTHORS for a list of authors.
All Rights Reserved
*/

import (
	"config"
	"os"
	"os/exec"
)

func init() {
	help := "Runs an interactive database shell"
	usage := `Usage: sql`
	name := "sql"

	main := func(shell *Shell, args []string) uint8 {
		cmd := exec.Command("psql", config.Get().Sql.GetSqlConnectionString())
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Run()
		return 0
	}

	registerShellCommand(help, usage, name, main)
}
