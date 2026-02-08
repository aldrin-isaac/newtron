package main

import "syscall"

// syscallExec replaces the current process with the given command.
func syscallExec(binary string, args []string, env []string) error {
	return syscall.Exec(binary, args, env)
}
