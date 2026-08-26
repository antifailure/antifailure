package cli

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

// A support bundle is a thing somebody sends to a stranger, which is the only
// fact about it that matters.
//
// So everything in it goes through the redactor on the way in rather than on
// the way out, and the bundle carries a manifest of exactly what it included,
// so the sender can see what they are about to send before they send it. A
// bundle that has to be trusted is a bundle nobody sends, and a report nobody
// sends is a bug nobody fixes.

// BundleEntry is one file in a bundle.
type BundleEntry struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	What  string `json:"what"`
}

// BundleJSON is the machine readable result.
type BundleJSON struct {
	Path     string        `json:"path"`
	Entries  []BundleEntry `json:"entries"`
	Redacted int           `json:"values_redacted"`
}

func newSupportCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "support",
		Short: "Collect a redacted diagnostic bundle",
	}
	cmd.AddCommand(newSupportBundleCommand(e))
	return cmd
}

func newSupportBundleCommand(e *Env) *cobra.Command {
	var branch, out string
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Write logs, decisions, the manifest, and doctor output, redacted",
		Long: strings.TrimSpace(`
Everything in the bundle goes through the redactor on the way in, and the
bundle lists exactly what it included so you can see what you are about to
send before you send it.

A bundle you have to trust is a bundle nobody sends, and a report nobody sends
is a bug nobody fixes.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := redact.New()
			o, m, err := orchestratorWithManifest(e, branch)
			if err != nil {
				return err
			}
			if out == "" {
				out = filepath.Join(".", fmt.Sprintf("af-support-%s.zip",
					e.Clock.Now().UTC().Format("20060102-150405")))
			}

			file, err := os.Create(out)
			if err != nil {
				return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
			}
			defer func() { _ = file.Close() }()

			archive := zip.NewWriter(file)
			var entries []BundleEntry

			add := func(name, what, body string) error {
				clean := r.String(body)
				w, wErr := archive.Create(name)
				if wErr != nil {
					return wErr
				}
				if _, wErr = w.Write([]byte(clean)); wErr != nil {
					return wErr
				}
				entries = append(entries, BundleEntry{Name: name, Bytes: len(clean), What: what})
				return nil
			}

			// The manifest, with the source URL variable's value never in it
			// because the manifest names the variable rather than the value.
			if body, mErr := json.MarshalIndent(m, "", "  "); mErr == nil {
				if err := add("manifest.json", "the effective configuration", string(body)); err != nil {
					return err
				}
			}
			if body := manifest.Explain(m); body != "" {
				if err := add("explain.txt", "every default resolved", body); err != nil {
					return err
				}
			}

			report := RunDoctor(cmd.Context(), e, systemProber{getenv: e.Getenv})
			if body, dErr := json.MarshalIndent(report, "", "  "); dErr == nil {
				if err := add("doctor.json", "what this machine looks like", string(body)); err != nil {
					return err
				}
			}

			if status, sErr := o.Status(cmd.Context()); sErr == nil {
				if body, jErr := json.MarshalIndent(status, "", "  "); jErr == nil {
					_ = add("status.json", "what was running", string(body))
				}
			}
			if lines, lErr := o.Logs(cmd.Context(), "", 500); lErr == nil {
				var b strings.Builder
				for _, l := range lines {
					fmt.Fprintf(&b, "%s\t%s\n", l.Service, l.Text)
				}
				_ = add("logs.txt", "the services' own output", b.String())
			}
			if decisions, dErr := o.Decisions(cmd.Context(), 500); dErr == nil {
				var b strings.Builder
				for _, d := range decisions {
					fmt.Fprintf(&b, "%s\t%s\t%s %s://%s%s\t%s\n",
						d.AtRaw, d.Mode, d.Method, schemeOf(d.TLS), d.Host, d.Path, d.Reason)
				}
				_ = add("decisions.txt", "every outbound request and what happened to it", b.String())
			}

			// Written last, so it describes the bundle it is inside.
			listing, _ := json.MarshalIndent(BundleJSON{
				Path: out, Entries: entries, Redacted: r.RegisteredCount(),
			}, "", "  ")
			if err := add("contents.json", "this listing", string(listing)); err != nil {
				return err
			}
			if err := archive.Close(); err != nil {
				return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(BundleJSON{Path: out, Entries: entries, Redacted: r.RegisteredCount()})
			}
			e.Out.Section("Support bundle")
			for _, entry := range entries {
				e.Out.Printf("  %-18s %-9s %s\n", entry.Name,
					humanBytes(uint64(entry.Bytes)), e.Out.S(StyleDim, entry.What))
			}
			e.Out.Printf("\n  Written to %s\n", e.Out.S(StyleBold, out))
			e.Out.Println(e.Out.Wrap(
				"  Everything above went through the redactor on the way in. Open it before you "+
					"send it; it is a zip and contents.json lists what is inside.", 2))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Where to write the bundle")
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to collect, defaulting to the checked out one")
	return cmd
}

func schemeOf(tls bool) string {
	if tls {
		return "https"
	}
	return "http"
}
