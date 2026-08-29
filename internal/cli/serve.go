package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"btw/internal/api"
	"btw/internal/app"
	"btw/internal/config"
	"btw/internal/nudge"
	"btw/internal/store"
	"btw/internal/webpush"
)

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the server",
		RunE:  func(cmd *cobra.Command, _ []string) error { return serve(cmd.Context()) },
	}
}

func serve(ctx context.Context) error {
	cfg, st, log, err := setup()
	if err != nil {
		return err
	}
	defer st.Close()

	main, derived, err := st.SchemaVersions(ctx)
	if err != nil {
		return err
	}
	log.Info("starting", "version", app.Version, "main_schema", main, "derived_schema", derived)

	if cfg.InsecurePublicURL() {
		// Two separate consequences, and the second is the one that breaks the product:
		// no browser registers a service worker against an insecure origin, so nothing
		// can subscribe at all.
		log.Warn("public url is http, so the session cookie ships without Secure and no browser will register a service worker",
			"url", cfg.PublicURL.String())
	}

	key, pub, err := st.VAPIDKeys(ctx)
	if err != nil {
		return err
	}
	// The contact URI push services are given. RFC 8292 allows an https URI, so this needs
	// no variable of its own and cannot go stale the way an operator's address would.
	sender := webpush.NewSender(key, pub, cfg.Origin())

	if err := bootstrap(ctx, cfg, st, log); err != nil {
		return err
	}

	spa, err := api.NewSPA(os.DirFS(cfg.WebDir))
	if err != nil {
		return fmt.Errorf("read %s: %w", cfg.WebDir, err)
	}
	if spa.Empty() {
		log.Warn("no frontend bundle found; serving the placeholder", "dir", cfg.WebDir)
	}

	scheduler := nudge.New(st, sender, log)
	srv := &http.Server{
		Addr:              app.ListenAddr,
		Handler:           api.New(cfg, st, log, sender, scheduler, spa).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	go scheduler.Run(ctx)

	errs := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", app.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// bootstrap creates the first administrator if there is none, and prints an invitation
// link.
//
// No default password exists at any point, so there is no credential for somebody to
// forget to change — and `btw invite` reprints one, which is the way back in when the
// first link scrolled out of a log.
func bootstrap(ctx context.Context, cfg *config.Config, st *store.Store, log logger) error {
	n, err := st.CountAdmins(ctx)
	if err != nil || n > 0 {
		return err
	}
	inv, token, err := st.CreateInvite(ctx, "", store.RoleAdmin)
	if err != nil {
		return err
	}
	log.Info("no administrator yet; open this link to make one", "expires_at", inv.ExpiresAt.Format(time.RFC3339))
	fmt.Println(cfg.Link("/invite/" + token))
	return nil
}

// logger is the little of log/slog this file uses, so bootstrap can be driven by a test
// that does not want output.
type logger interface {
	Info(msg string, args ...any)
}
