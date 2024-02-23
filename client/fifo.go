package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/katexochen/sync/api"
	"github.com/spf13/cobra"
)

func newFifoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fifo",
		Short: "First-in, first-out queue",
	}
	cmd.PersistentFlags().StringP("endpoint", "e", "http://localhost:8080", "endpoint of the sync server")
	cmd.PersistentFlags().StringP("output", "o", "raw", "output format: raw, json")
	cmd.AddCommand(
		newFifoNewCommand(),
		newFifoTicketCommand(),
		newFifoWaitCommand(),
		newFifoDoneCommand(),
	)
	return cmd
}

func newFifoNewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "create a new first-in, first-out queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags, err := parseFlagsNew(cmd)
			if err != nil {
				return fmt.Errorf("parsing flags: %w", err)
			}
			out, err := RunFifoNew(cmd.Context(), flags)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	return cmd
}

func RunFifoNew(ctx context.Context, flags *flagsNew) (string, error) {
	url, err := urlJoin(flags.endpoint, "fifo", "new")
	if err != nil {
		return "", err
	}

	resp := &api.FifoNewResponse{}
	if err := newHTTPClient().RequestJSON(ctx, url, http.NoBody, resp); err != nil {
		return "", err
	}

	if flags.output == "json" {
		b, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return resp.UUID.String(), nil
}

func newFifoTicketCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ticket",
		Short: "request a ticket for the given fifo queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags, err := parseFlagsNew(cmd)
			if err != nil {
				return fmt.Errorf("parsing flags: %w", err)
			}
			out, err := RunFifoTicket(cmd.Context(), flags)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringP("uuid", "u", "", "uuid of the fifo queue")
	return cmd
}

func RunFifoTicket(ctx context.Context, flags *flagsNew) (string, error) {
	url, err := urlJoin(flags.endpoint, "fifo", flags.uuid, "ticket")
	if err != nil {
		return "", err
	}

	resp := &api.FifoTicketResponse{}
	if err := newHTTPClient().RequestJSON(ctx, url, http.NoBody, resp); err != nil {
		return "", err
	}

	if flags.output == "json" {
		b, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return resp.TicketID.String(), nil
}

func newFifoWaitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "wait for the ticket to be called",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags, err := parseFlagsNew(cmd)
			if err != nil {
				return fmt.Errorf("parsing flags: %w", err)
			}
			return RunFifoWait(cmd.Context(), flags)
		},
	}
	cmd.Flags().StringP("uuid", "u", "", "uuid of the fifo queue")
	must(cmd.MarkFlagRequired("uuid"))
	cmd.Flags().StringP("ticket", "t", "", "uuid of the ticket")
	must(cmd.MarkFlagRequired("ticket"))
	return cmd
}

func RunFifoWait(ctx context.Context, flags *flagsNew) error {
	url, err := urlJoin(flags.endpoint, "fifo", flags.uuid, "wait", flags.ticketID)
	if err != nil {
		return err
	}

	return newHTTPClient().Get(ctx, url)
}

func newFifoDoneCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done",
		Short: "mark the ticket as done",
		RunE: func(cmd *cobra.Command, args []string) error {
			flags, err := parseFlagsNew(cmd)
			if err != nil {
				return fmt.Errorf("parsing flags: %w", err)
			}
			return RunFifoDone(cmd.Context(), flags)
		},
	}
	cmd.Flags().StringP("uuid", "u", "", "uuid of the fifo queue")
	must(cmd.MarkFlagRequired("uuid"))
	cmd.Flags().StringP("ticket", "t", "", "uuid of the ticket")
	must(cmd.MarkFlagRequired("ticket"))
	return cmd
}

func RunFifoDone(ctx context.Context, flags *flagsNew) error {
	url, err := urlJoin(flags.endpoint, "fifo", flags.uuid, "done", flags.ticketID)
	if err != nil {
		return err
	}

	return newHTTPClient().Get(ctx, url)
}

type flagsNew struct {
	endpoint string
	output   string
	uuid     string
	ticketID string
}

func parseFlagsNew(cmd *cobra.Command) (*flagsNew, error) {
	endpoint, err := cmd.Flags().GetString("endpoint")
	if err != nil {
		return nil, err
	}
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return nil, err
	}

	// Optional flags
	uuid, _ := cmd.Flags().GetString("uuid")
	ticketID, _ := cmd.Flags().GetString("ticket")

	return &flagsNew{
		endpoint: endpoint,
		output:   output,
		uuid:     uuid,
		ticketID: ticketID,
	}, nil
}

func urlJoin(base string, pathSegments ...string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parsing endpoint: %w", err)
	}
	return u.JoinPath(pathSegments...).String(), nil
}
