// browser-ctl v2: daemon + MCP server + CLI in one static Go binary.
// No Node, no MCP dependency — MCP is JSON-RPC over stdio, implemented here.
//
// Usage:
//
//	browser-ctl daemon [--port N] [--stderr]   long-lived daemon
//	browser-ctl mcp [--caps cdp] [--port N]    MCP server over stdio
//	browser-ctl run [--port N] "goto https://example.com"
//	browser-ctl fill-login [--port N] <domain>  fill the active page's login form
//	                                            with a vault payload read from stdin
//	browser-ctl run --port N --commands-file commands.txt
//	browser-ctl tabs [--port N]
//	browser-ctl status
//	browser-ctl shutdown
//	browser-ctl serve [--port N] [--no-wait]   legacy relay-only mode
//	browser-ctl help
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/thovilabs/browser-ctl/internal/client"
	"github.com/thovilabs/browser-ctl/internal/daemon"
	"github.com/thovilabs/browser-ctl/internal/mcp"
	"github.com/thovilabs/browser-ctl/internal/relay"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "daemon":
		fs := flag.NewFlagSet("daemon", flag.ExitOnError)
		port := fs.Int("port", 0, "relay port (default: first free of 9010, 9011, 9012)")
		fs.Parse(os.Args[2:])
		d, err := daemon.Start(*port, logf)
		if err != nil {
			fatal(err)
		}
		logf("browser-ctl %s daemon on 127.0.0.1:%d (pid %d)", daemon.Version(), d.Port, daemon.Pid())
		os.Exit(exitCode(d.Wait()))
	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ExitOnError)
		port := fs.Int("port", 0, "daemon port (0 = find or start)")
		caps := fs.String("caps", "", "comma-separated capabilities (cdp)")
		fs.Parse(os.Args[2:])
		var capList []string
		if *caps != "" {
			capList = strings.Split(*caps, ",")
		}
		c, _, err := client.FindOrStart(*port, capList)
		if err != nil {
			fatal(err)
		}
		defer c.Close()
		capsMap := map[string]bool{}
		for _, cp := range capList {
			capsMap[strings.TrimSpace(cp)] = true
		}
		if err := mcp.New(c, capsMap).Serve(); err != nil {
			fatal(err)
		}
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		port := fs.Int("port", 0, "daemon port (0 = find or start)")
		file := fs.String("commands-file", "", "read the commands from this file instead of the argument")
		caps := fs.String("caps", "", "comma-separated capabilities (cdp)")
		fs.Parse(os.Args[2:])
		commands, err := readCommands(*file, fs.Args())
		if err != nil {
			fatal(err)
		}
		var capList []string
		if *caps != "" {
			capList = strings.Split(*caps, ",")
		}
		out, err := client.RunCommands(*port, commands, capList)
		if out != "" {
			fmt.Println(out)
		}
		if err != nil {
			fatal(err)
		}
	case "fill-login":
		if err := runFillLogin(os.Args[2:]); err != nil {
			var ue *usageError
			if errors.As(err, &ue) {
				fmt.Fprintln(os.Stderr, "browser-ctl:", err)
				os.Exit(2)
			}
			fatal(err)
		}
	case "tabs":
		fs := flag.NewFlagSet("tabs", flag.ExitOnError)
		port := fs.Int("port", 0, "daemon port (0 = find or start)")
		fs.Parse(os.Args[2:])
		out, err := client.RunCommands(*port, "tab list", nil)
		if out != "" {
			fmt.Println(out)
		}
		if err != nil {
			fatal(err)
		}
	case "status":
		info := daemon.FindDaemon(daemon.HomeDir())
		if info == nil {
			if p, st := daemon.ProbePorts(0); st != nil {
				fmt.Printf("a browsermcp server (pid %d) answers on 127.0.0.1:%d, but no usable daemon.json — not a daemon this home can drive\n", st.Pid, p)
				return
			}
			fmt.Println("no daemon is running")
			return
		}
		fmt.Printf("daemon on 127.0.0.1:%d (pid %d, version %s)\n", info.Port, info.Pid, info.Version)
	case "shutdown":
		fs := flag.NewFlagSet("shutdown", flag.ExitOnError)
		port := fs.Int("port", 0, "daemon port")
		fs.Parse(os.Args[2:])
		if err := client.Shutdown(*port); err != nil {
			fatal(err)
		}
		fmt.Println("daemon stopped")
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		port := fs.Int("port", 0, "relay port (default: first free of 9010, 9011, 9012)")
		noWait := fs.Bool("no-wait", false, "exit once the relay is up, without waiting for a browser")
		fs.Parse(os.Args[2:])
		if err := runServe(*port, !*noWait); err != nil {
			fatal(err)
		}
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runServe(port int, wait bool) error {
	key, err := relay.LoadIdentity(daemon.HomeDir())
	if err != nil {
		return err
	}
	server, boundPort, err := relay.StartServer(port, key, daemon.Version(), logf)
	if err != nil {
		return err
	}
	defer server.Close()
	logf("browser-ctl %s relay listening on 127.0.0.1:%d", daemon.Version(), boundPort)
	if !wait {
		logf("relay running (Ctrl+C to stop)")
		select {}
	}
	logf("waiting for the Browser MCP extension to connect...")
	for {
		if b := server.FirstBrowser(); b != nil {
			logf("browser connected: %s (pairing %s)", b.Conn.Hello().Browser.Name, b.Conn.State().Pairing)
			return nil
		}
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

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "browser-ctl:", err)
	os.Exit(1)
}

func exitCode(reason string) int {
	_ = reason
	return 0
}

func usage() {
	fmt.Fprint(os.Stderr, `browser-ctl v2: drive Chrome through the Browser MCP extension (no MCP server, no Node)

Usage:
  browser-ctl daemon [--port N]             start the long-lived daemon
  browser-ctl mcp [--caps cdp] [--port N]   MCP server over stdio
  browser-ctl run [flags] "cmd1
cmd2"       run a batch of browser commands
  browser-ctl run --commands-file file.txt  read the commands from a file
  browser-ctl fill-login [--port N] <domain>  fill the active page's login form with
                                            the vault payload (JSON) read from stdin
  browser-ctl tabs [--port N]               list the connected browser's tabs
  browser-ctl status                        show the running daemon
  browser-ctl shutdown [--port N]           stop the daemon
  browser-ctl serve [--port N] [--no-wait]  legacy: relay only, wait for a browser
  browser-ctl help                          this help

Commands (one per line): goto, back, forward, reload, click, hover, scroll, drag, fill,
type, press, select, check, uncheck, upload, wait, dialog, tab, browser, snapshot, find,
get, screenshot, pdf, download, eval, console, requests, request, cdp, help.
`)
}
