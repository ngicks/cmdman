package commands

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"github.com/ngicks/crabswarm/pkg/cmdman"
	"github.com/ngicks/crabswarm/pkg/cmdman/store"
	"github.com/spf13/cobra"
)

const (
	defaultLsHeader    = "ID\tNAME\tSTATE\tEXIT CODE\tCOMMAND"
	defaultLsRowFormat = "{{slice .ID 0 12}}\t{{.Name}}\t{{.State}}\t{{if .ExitCode}}{{printf \"%d\" .ExitCode}}{{else}}-{{end}}\t{{command .}}"
)

const commandMaxLen = 40

var lsFuncMap = template.FuncMap{
	"json": func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("ERR: %v", err)
		}
		return string(b)
	},
	"command": func(e store.CommandEntry) string {
		if e.ConfigJSON == nil || len(e.ConfigJSON.Argv) == 0 {
			return "-"
		}
		s := strings.Join(e.ConfigJSON.Argv, " ")
		if len(s) > commandMaxLen {
			return s[:commandMaxLen-3] + "..."
		}
		return s
	},
}

func init() {
	rootCmd.AddCommand(lsCmd)
	lsCmd.Flags().StringArrayP("label", "l", nil, "Filter by label KEY=VALUE (repeatable)")
	lsCmd.Flags().BoolP("all", "a", false, "Show all (including exited)")
	lsCmd.Flags().BoolP("quiet", "q", false, "Print IDs only")
	lsCmd.Flags().String("format", "", buildFormatUsage())
}

var lsCmd = &cobra.Command{
	Use:   "ls [flags]",
	Short: "List commands",
	RunE:  runLs,
}

func runLs(cmd *cobra.Command, args []string) error {
	labelSlice, _ := cmd.Flags().GetStringArray("label")
	allStates, _ := cmd.Flags().GetBool("all")
	quiet, _ := cmd.Flags().GetBool("quiet")
	format, _ := cmd.Flags().GetString("format")

	labels, err := parseLabels(labelSlice)
	if err != nil {
		return err
	}

	svc, err := cmdmanService()
	if err != nil {
		return err
	}
	entries, err := svc.List(cmd.Context(), cmdman.ListRequest{
		AllStates: allStates,
		Labels:    labels,
	})
	if err != nil {
		return err
	}

	if quiet {
		for _, e := range entries {
			fmt.Fprintln(cmd.OutOrStdout(), e.ID)
		}
		return nil
	}

	if format == "" {
		format = defaultLsRowFormat
		fmt.Fprintln(cmd.OutOrStdout(), defaultLsHeader)
	}

	tmpl, err := template.New("format").Funcs(lsFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format template: %w", err)
	}
	out := cmd.OutOrStdout()
	for _, e := range entries {
		if err := tmpl.Execute(out, e); err != nil {
			return fmt.Errorf("execute format template: %w", err)
		}
		fmt.Fprintln(out)
	}
	return nil
}

func buildFormatUsage() string {
	t := reflect.TypeOf(store.CommandEntry{})
	var fields []string
	for i := range t.NumField() {
		f := t.Field(i)
		fields = append(fields, fmt.Sprintf(".%s (%s)", f.Name, f.Type))
	}
	return fmt.Sprintf(
		"Go text/template string. Available fields:\n  %s\nTemplate functions: json",
		strings.Join(fields, ", "),
	)
}

func parseLabels(labelSlice []string) (map[string]string, error) {
	if len(labelSlice) == 0 {
		return nil, nil
	}
	labels := make(map[string]string)
	for _, l := range labelSlice {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label format: %s (expected KEY=VALUE)", l)
		}
		labels[k] = v
	}
	return labels, nil
}
