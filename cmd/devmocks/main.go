// Command devmocks serves development-only mocks of Bluesky, Mastodon and
// ShortLinks, and seeds a demo LED Programming workshop for the outreach
// screens (#0580). It binds loopback addresses only and is never installed by
// scripts/deploy.sh or scripts/release.sh.
//
//	devmocks serve [-addr 127.0.0.1:18081] [-public-url http://127.0.0.1:18081]
//	devmocks seed  -mock-url http://127.0.0.1:18081 [-media-dir .scratch/demo/media]
//
// seed reads DATABASE_URL and refuses any host but localhost. The magic
// credential "bad-token" makes every mock answer 401.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/devmocks"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: devmocks serve|seed [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "seed":
		err = seed(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		log.SetFlags(0)
		log.Fatalf("devmocks: %v", err)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:18081", "loopback listen address")
	public := fs.String("public-url", "", "base URL clients reach the mocks at (default http://<addr>)")
	_ = fs.Parse(args)
	if err := devmocks.RequireLoopback(*addr); err != nil {
		return err
	}
	if *public == "" {
		*public = "http://" + *addr
	}
	srv := &http.Server{Addr: *addr, Handler: devmocks.New(*public, nil, nil).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		_ = srv.Close()
	}()
	log.Printf("devmocks listening on %s (public %s); Bluesky /bluesky, Mastodon /mastodon, ShortLinks /shortlinks; token %q is rejected", *addr, *public, devmocks.BadToken)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func seed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	mockURL := fs.String("mock-url", "http://127.0.0.1:18081", "base URL of the running mocks")
	mediaDir := fs.String("media-dir", ".scratch/demo/media", "directory the server's MEDIA_DIR points at")
	_ = fs.Parse(args)
	dsn := os.Getenv("DATABASE_URL")
	if err := devmocks.RequireLocalDSN(dsn); err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := devmocks.SeedDemo(ctx, pool, *mediaDir, strings.TrimRight(*mockURL, "/"), time.Now()); err != nil {
		return err
	}
	fmt.Println("Seeded demo workshop", devmocks.DemoSlug, "and saved the mock connection values.")
	return nil
}
