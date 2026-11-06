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
	"strconv"
	"strings"
	"time"

	"gitreview/internal/git"
	"gitreview/internal/server"
)

//go:embed web
var webFiles embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Chyba:", err)
		os.Exit(1)
	}
}

func run() error {
	port := flag.Int("p", 8765, "port")
	bind := flag.String("b", "0.0.0.0", "adresa, na které server poslouchá")
	allow := flag.String("allow-host", "", "další povolené názvy hostitele oddělené čárkou (IP adresy, localhost a hostname stroje jsou povolené vždy)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Použití: %s [-p port] [-b adresa] [-allow-host jméno,…] [cesta k repozitáři]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

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
