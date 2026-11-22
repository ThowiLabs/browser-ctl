// browser-ctl drives the user's own Chrome through the Browser MCP extension, without any
// MCP server: a standalone, statically linked CLI. Editors without MCP support can run it as
// a subprocess and read JSON/text from stdout.
//
// Usage:
//
//	browser-ctl serve [--port N] [--no-wait]
//	browser-ctl run [--port N] "goto https://example.com"
//	browser-ctl run --port N --commands-file commands.txt
//	browser-ctl tabs [--port N]
//	browser-ctl status
//	browser-ctl help
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/thovilabs/browser-ctl/internal/cmd"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		port := fs.Int("port", 0, "relay port (default: first free of 9010, 9011, 9012)")
		noWait := fs.Bool("no-wait", false, "exit once the relay is up, without waiting for a browser")
		fs.Parse(os.Args[2:])
		if err := cmd.RunServe(*port, !*noWait); err != nil {
			fatal(err)
		}
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		port := fs.Int("port", 0, "relay port (default: first free of 9010, 9011, 9012)")
		file := fs.String("commands-file", "", "read the commands from this file instead of the argument")
		fs.Parse(os.Args[2:])
		commands, err := readCommands(*file, fs.Args())
		if err != nil {
			fatal(err)
		}
		if err := cmd.RunExec(commands, *port); err != nil {
			fatal(err)
		}
	case "tabs":
		fs := flag.NewFlagSet("tabs", flag.ExitOnError)
		port := fs.Int("port", 0, "relay port")
		fs.Parse(os.Args[2:])
		if err := cmd.RunTabs(*port); err != nil {
			fatal(err)
		}
	case "status":
		cmd.RunStatus()
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func readCommands(file string, args []string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if len(args) == 0 {
		return readStdin()
	}
	return strings.Join(args, "\n"), nil
}

func readStdin() (string, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "", fmt.Errorf("no commands given (pass them as arguments, --commands-file, or on stdin)")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func usage() {
	fmt.Fprint(os.Stderr, `browser-ctl: drive Chrome through the Browser MCP extension (no MCP server)

Usage:
  browser-ctl serve [--port N] [--no-wait]     start the relay and wait for a browser
  browser-ctl run [flags] "cmd1
cmd2"       run a batch of browser commands
  browser-ctl run --commands-file file.txt     read the commands from a file
  browser-ctl tabs [--port N]                  list the connected browser's tabs
  browser-ctl status                           show the relay identity and ports
  browser-ctl help                             this help

run flags:
  --port N          relay port (default: first free of 9010, 9011, 9012)
  --commands-file   read the commands from this file

Commands (one per line): goto, back, forward, reload, click, hover, scroll, fill, type,
press, select, check, uncheck, wait, snapshot, find, get, screenshot, eval, tab, help.
Targets: e12 (snapshot refs), getByRole('button', {name:'Go'}), getByText('...'), or CSS.
`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "browser-ctl:", err)
	os.Exit(1)
}

var _ = strconv.Itoa
