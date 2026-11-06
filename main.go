// Command gitreview serves a web UI for reviewing the changes of the current
// git branch against develop/master with an IntelliJ-like side-by-side diff.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/JanVrana/gitreview/internal/git"
	"github.com/JanVrana/gitreview/internal/server"
)

//go:embed web
var webFiles embed.FS

// version is set at release build time via -ldflags "-X main.version=…".
var version = "dev"

// buildVersion returns the release version, or the module version for
// binaries built with `go install …@vX`.
func buildVersion() string {
	if version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			return bi.Main.Version
		}
	}
	return version
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run() error {
	port := flag.Int("p", 8765, "port")
	bind := flag.String("b", "0.0.0.0", "listen address")
	allow := flag.String("allow-host", "", "extra allowed host names, comma separated (IP addresses, localhost and the machine hostname are always allowed)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-p port] [-b address] [-allow-host name,…] [repository path]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("gitreview", buildVersion())
		return nil
	}

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	repo, err := git.Open(dir)
	if err != nil {
		return err
	}
	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}
	srv, err := server.New(repo, web, allowedHosts(*allow))
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(*bind, strconv.Itoa(*port)))
	if err != nil {
		return err
	}
	fmt.Printf("gitreview: %s\n", repo.Root)
	for _, u := range urls(*bind, *port) {
		fmt.Printf("  → %s\n", u)
	}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return hs.Serve(ln)
}

// allowedHosts returns host names accepted in the Host header besides IP
// literals and localhost: the machine's hostname and user-supplied names.
func allowedHosts(extra string) []string {
	var hosts []string
	if h, err := os.Hostname(); err == nil && h != "" {
		short, _, _ := strings.Cut(h, ".")
		hosts = append(hosts, h, short, short+".local")
	}
	for h := range strings.SplitSeq(extra, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// urls lists addresses the UI is reachable on.
func urls(bind string, port int) []string {
	p := strconv.Itoa(port)
	if bind != "0.0.0.0" && bind != "" && bind != "::" {
		return []string{"http://" + net.JoinHostPort(bind, p)}
	}
	var res []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.To4() == nil || ipn.IP.IsLoopback() {
			continue
		}
		res = append(res, "http://"+net.JoinHostPort(ipn.IP.String(), p))
	}
	return append(res, "http://"+net.JoinHostPort("localhost", p))
}
