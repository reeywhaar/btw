package cli

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"btw/internal/app"
	"btw/internal/store"
)

// inviteCmd is the way back in. The token is readable exactly once, when it is minted, so
// a lost link is reissued rather than recovered.
func inviteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Print a new invitation link",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, st, _, err := setup()
			if err != nil {
				return err
			}
			defer st.Close()

			role, _ := cmd.Flags().GetString("role")
			inv, token, err := st.CreateInvite(cmd.Context(), "", role)
			if err != nil {
				return err
			}
			cmd.Println(cfg.Link("/invite/" + token))
			cmd.PrintErrf("expires %s\n", inv.ExpiresAt.Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().String("role", store.RoleUser, "admin or user")
	return cmd
}

// healthcheckCmd is what the image's HEALTHCHECK runs, so the container needs no HTTP
// client of its own and a wedged process fails it.
func healthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Ask the running server whether it is well",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get("http://127.0.0.1" + app.ListenAddr + "/healthz")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthz answered %s", resp.Status)
			}
			return nil
		},
	}
}
