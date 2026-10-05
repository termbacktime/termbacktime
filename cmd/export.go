package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
)

func newExport(o *options) *cobra.Command {
	var format, output string
	c := &cobra.Command{Use: "export <id|path> --output PATH", Short: "Export a text or Markdown transcript", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if format != "txt" && format != "md" {
			return fmt.Errorf("supported export formats: txt, md")
		}
		if output == "" {
			return fmt.Errorf("--output is required")
		}
		destination, err := config.ExpandPath(output)
		if err != nil {
			return err
		}
		lib, err := o.library()
		if err != nil {
			return err
		}
		path, err := lib.Resolve(args[0])
		if err != nil {
			return err
		}
		r, err := recording.Load(path)
		if err != nil {
			return err
		}
		if err := recording.Publish(destination, func(w io.Writer) error {
			return recording.WriteTranscript(c.Context(), w, r, format == "md")
		}); err != nil {
			return err
		}
		fmt.Fprintln(c.OutOrStdout(), destination)
		return nil
	}}
	c.Flags().StringVar(&format, "format", "txt", "export format (txt, md)")
	c.Flags().StringVarP(&output, "output", "o", "", "output transcript filename")
	return c
}
