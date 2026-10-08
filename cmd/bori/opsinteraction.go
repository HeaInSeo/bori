package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	opsv1 "github.com/HeaInSeo/bori/apis/ops/v1alpha1"
	"github.com/HeaInSeo/bori/pkg/interaction"
)

// cmdOps dispatches the read-only operational subcommands.
func cmdOps(args []string) {
	if len(args) < 1 || args[0] != "interaction" {
		fmt.Fprintln(os.Stderr, "usage: bori ops interaction [-f <file>|-] [-o text|json]")
		os.Exit(1)
	}
	if err := runOpsInteraction(args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "bori ops interaction:", err)
		os.Exit(1)
	}
}

// runOpsInteraction renders the interaction summaries of OperationalTargets
// read from `kubectl get operationaltargets -o json` output (a List or a
// single object). It only formats what the statuses hold: no cluster call,
// no other object, no action.
func runOpsInteraction(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("ops interaction", flag.ContinueOnError)
	file := fs.String("f", "-", "kubectl JSON output to read ('-' for stdin)")
	format := fs.String("o", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in := stdin
	if *file != "-" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	ts, err := readTargets(in)
	if err != nil {
		return err
	}
	switch *format {
	case "text":
		_, err = io.WriteString(stdout, interaction.RenderText(ts))
		return err
	case "json":
		type row struct {
			Namespace   string                   `json:"namespace"`
			Name        string                   `json:"name"`
			Interaction *opsv1.InteractionStatus `json:"interaction"`
		}
		out := struct {
			Overview interaction.Overview `json:"overview"`
			Targets  []row                `json:"targets"`
		}{Overview: interaction.OverviewOf(ts), Targets: []row{}}
		for _, t := range ts {
			out.Targets = append(out.Targets, row{t.Namespace, t.Name, t.Status.Interaction})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	return fmt.Errorf("unknown output format %q", *format)
}

func readTargets(r io.Reader) ([]opsv1.OperationalTarget, error) {
	b, err := io.ReadAll(io.LimitReader(r, 64<<20))
	if err != nil {
		return nil, err
	}
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("input is not kubectl JSON: %w", err)
	}
	switch head.Kind {
	case "OperationalTarget":
		var t opsv1.OperationalTarget
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, err
		}
		return []opsv1.OperationalTarget{t}, nil
	case "OperationalTargetList", "List":
		var l struct {
			Items []opsv1.OperationalTarget `json:"items"`
		}
		if err := json.Unmarshal(b, &l); err != nil {
			return nil, err
		}
		for _, t := range l.Items {
			if t.Kind != "" && t.Kind != "OperationalTarget" {
				return nil, fmt.Errorf("item kind %q is not OperationalTarget", t.Kind)
			}
		}
		return l.Items, nil
	}
	return nil, errors.New("expected an OperationalTarget or a list of them")
}
