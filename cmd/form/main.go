// Command form works hanzoai/form from a terminal.
//
//	form import -data DIR -org ORG [-store NAME] [-api URL] [-cursor FILE] [-file] [-dry]
//
// import brings commerce's forms, subscribers and submissions into /v1/form.
// TOKEN (or -token) is the IAM bearer of an admin of the org they land in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout)
	stop()
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "form:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "import" {
		return fmt.Errorf("usage: form import -data DIR -org ORG [-store NAME] [-api URL] [-cursor FILE] [-file] [-dry]")
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	data := fs.String("data", "", "commerce's data directory (cloud's is $CLOUD_DATA_DIR/commerce); point it at a copy")
	org := fs.String("org", "", "the commerce org whose forms are read: its namespace")
	store := fs.String("store", "", "the commerce store read: the org's own (the default), or system, the one commerce's form routes wrote")
	api := fs.String("api", "https://api.hanzo.ai", "the API the forms are written to")
	token := fs.String("token", "", "IAM bearer of an admin of the org the forms land in; default $TOKEN")
	cursor := fs.String("cursor", "form.cursor", "the resume cursor, written after every acknowledged batch and removed when the run ends")
	file := fs.Bool("file", false, "also file each imported response in the CRM")
	dry := fs.Bool("dry", false, "count what would be imported and write nothing")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *data == "" || *org == "" {
		return fmt.Errorf("-data and -org are required")
	}
	if *store == "" {
		*store = *org
	}
	if !*dry {
		if *token == "" {
			*token = os.Getenv("TOKEN")
		}
		if *token == "" {
			return fmt.Errorf("TOKEN is required")
		}
		u, err := url.Parse(*api)
		if err != nil || u.Host == "" || u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
			return fmt.Errorf("-api is an https address")
		}
	}
	src, err := openCommerce(ctx, *data, *store, *org)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	if *dry {
		return count(src, out)
	}
	im := &importer{
		src:    src,
		name:   "commerce/" + *store + "/" + *org,
		api:    &client{ctx: ctx, base: *api, token: *token, org: *org, http: &http.Client{Timeout: 2 * time.Minute}, pause: time.Second, tries: 10},
		cursor: *cursor,
		file:   *file,
		out:    out,
	}
	return im.run()
}

// loopback reports whether host is this machine, the one place plain http may
// carry the bearer.
func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}
