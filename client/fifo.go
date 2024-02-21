package main

import (
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
		RunE:  runFifoNew,
	}
	return cmd
}

func runFifoNew(cmd *cobra.Command, _ []string) error {
	flags, err := parseFlagsNew(cmd)
	if err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}

	url, err := url.JoinPath(flags.endpoint, "fifo", "new")
	if err != nil {
		return err
	}

	resp := &api.FifoNewResponse{}
	if err := newHTTPClient().RequestJSON(
		cmd.Context(), url, http.NoBody, resp,
	); err != nil {
		return err
	}

	if flags.output == "json" {
		b, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	} else {
		fmt.Println(resp.UUID)
	}

	return nil
}

func newFifoTicketCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ticket",
		Short: "request a ticket for the given fifo queue",
		RunE:  runFifoTicket,
	}
	cmd.Flags().StringP("uuid", "u", "", "uuid of the fifo queue")
	return cmd
}

func runFifoTicket(cmd *cobra.Command, _ []string) error {
	flags, err := parseFlagsNew(cmd)
	if err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}

	url, err := url.JoinPath(flags.endpoint, "fifo", flags.uuid, "ticket")
	if err != nil {
		return err
	}

	resp := &api.FifoTicketResponse{}
	if err := newHTTPClient().RequestJSON(
		cmd.Context(), url, http.NoBody, resp,
	); err != nil {
		return err
	}

	if flags.output == "json" {
		b, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	} else {
		fmt.Println(resp.TicketID)
	}

	return nil
}

func newFifoWaitCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "wait for the ticket to be called",
		RunE:  runFifoWait,
	}
	cmd.Flags().StringP("uuid", "u", "", "uuid of the fifo queue")
	must(cmd.MarkFlagRequired("uuid"))
	cmd.Flags().StringP("ticket", "t", "", "uuid of the ticket")
	must(cmd.MarkFlagRequired("ticket"))
	return cmd
}

func runFifoWait(cmd *cobra.Command, args []string) error {
	flags, err := parseFlagsNew(cmd)
	if err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}

	url, err := url.JoinPath(flags.endpoint, "fifo", flags.uuid, "wait", flags.ticketID)
	if err != nil {
		return err
	}

	return newHTTPClient().Get(cmd.Context(), url)
}

func newFifoDoneCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done",
		Short: "mark the ticket as done",
		RunE:  runFifoDone,
	}
	cmd.Flags().StringP("uuid", "u", "", "uuid of the fifo queue")
	must(cmd.MarkFlagRequired("uuid"))
	cmd.Flags().StringP("ticket", "t", "", "uuid of the ticket")
	must(cmd.MarkFlagRequired("ticket"))
	return cmd
}

func runFifoDone(cmd *cobra.Command, args []string) error {
	flags, err := parseFlagsNew(cmd)
	if err != nil {
		return fmt.Errorf("parsing flags: %w", err)
	}

	url, err := url.JoinPath(flags.endpoint, "fifo", flags.uuid, "done", flags.ticketID)
	if err != nil {
		return err
	}

	return newHTTPClient().Get(cmd.Context(), url)
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
