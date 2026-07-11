package cli

import (
	"encoding/json"
	"fmt"

	"github.com/akira-toriyama/rundiff/internal/version"
	"github.com/spf13/cobra"
)

// versionLine is the human build-identity line for --version.
func versionLine() string { return version.Get().Human() }

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Long: "Print rundiff's build identity. With --json, emit it as a single JSON object\n" +
			"(version, commit, date, go) for scripts and agents.",
		Example: "  rundiff version\n  rundiff version --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			if asJSON {
				// The single JSON funnel: HTML escaping off so any <, >, & pass
				// through verbatim. Encode adds a trailing newline.
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetEscapeHTML(false)
				if err := enc.Encode(info); err != nil {
					return &exitError{code: codeRundiff, msg: "marshalling version: " + err.Error()}
				}
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rundiff %s\n", info.Human())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output build info as JSON")
	return cmd
}
