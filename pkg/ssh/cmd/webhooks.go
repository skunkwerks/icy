package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2/table"
	"github.com/dustin/go-humanize"
	"github.com/google/uuid"
	"github.com/skunkwerks/icy/pkg/access"
	"github.com/skunkwerks/icy/pkg/backend"
	"github.com/skunkwerks/icy/pkg/proto"
	"github.com/skunkwerks/icy/pkg/utils"
	"github.com/skunkwerks/icy/pkg/webhook"
	"github.com/spf13/cobra"
)

func webhookCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "webhook",
		Aliases: []string{"webhooks"},
		Short:   "Manage repository webhooks",
	}

	cmd.AddCommand(
		webhookListCommand(),
		webhookInfoCommand(),
		webhookCreateCommand(),
		webhookDeleteCommand(),
		webhookUpdateCommand(),
		webhookDeliveriesCommand(),
	)

	return cmd
}

var webhookEvents []string

func init() {
	events := webhook.Events()
	webhookEvents = make([]string, len(events))
	for i, e := range events {
		webhookEvents[i] = e.String()
	}
}

func webhookListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "list REPOSITORY",
		Short:             "List repository webhooks",
		Args:              cobra.ExactArgs(1),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			webhooks, err := be.ListWebhooks(ctx, repo)
			if err != nil {
				return err
			}

			table := table.New().Headers("ID", "URL", "Events", "Format", "Auth", "Active", "Created At", "Updated At")
			for _, h := range webhooks {
				events := make([]string, len(h.Events))
				for i, e := range h.Events {
					events[i] = e.String()
				}

				table = table.Row(
					strconv.FormatInt(h.ID, 10),
					utils.Sanitize(h.URL),
					strings.Join(events, ","),
					webhook.FormatOf(h.Format).String(),
					webhookAuth(h),
					strconv.FormatBool(h.Active),
					humanize.Time(h.CreatedAt),
					humanize.Time(h.UpdatedAt),
				)
			}
			cmd.Println(table)
			return nil
		},
	}

	return cmd
}

func webhookCreateCommand() *cobra.Command {
	var events []string
	var secret string
	var active bool
	var contentType string
	var format string
	var bearerStdin bool
	cmd := &cobra.Command{
		Use:               "create REPOSITORY URL",
		Short:             "Create a repository webhook",
		Args:              cobra.ExactArgs(2),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			var evs []webhook.Event
			for _, e := range events {
				ev, err := webhook.ParseEvent(e)
				if err != nil {
					return fmt.Errorf("invalid event: %w", err)
				}

				evs = append(evs, ev)
			}

			var ct webhook.ContentType
			switch strings.ToLower(strings.TrimSpace(contentType)) {
			case "json":
				ct = webhook.ContentTypeJSON
			case "form":
				ct = webhook.ContentTypeForm
			default:
				return webhook.ErrInvalidContentType
			}

			opts := backend.WebhookOptions{}
			if opts.Format, err = webhook.ParseFormat(format); err != nil {
				return err
			}
			if bearerStdin {
				token, err := readBearerToken(cmd)
				if err != nil {
					return err
				}
				opts.BearerToken = &token
			}

			url := utils.Sanitize(args[1])
			return be.CreateWebhook(ctx, repo, strings.TrimSpace(url), ct, secret, evs, active, opts)
		},
	}

	cmd.Flags().StringSliceVarP(&events, "events", "e", nil, fmt.Sprintf("events to trigger the webhook, available events are (%s)", strings.Join(webhookEvents, ", ")))
	cmd.Flags().StringVarP(&secret, "secret", "s", "", "secret to sign the webhook payload")
	cmd.Flags().BoolVarP(&active, "active", "a", true, "whether the webhook is active")
	cmd.Flags().StringVarP(&contentType, "content-type", "c", "json", "content type of the webhook payload, can be either `json` or `form`")
	cmd.Flags().StringVar(&format, "format", "icy", "payload format, can be either `icy` or `buildkite`")
	cmd.Flags().BoolVar(&bearerStdin, "bearer-stdin", false, "read a bearer token from stdin and send it in the Authorization header (server admins only)")

	return cmd
}

func webhookDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "delete REPOSITORY WEBHOOK_ID",
		Short:             "Delete a repository webhook",
		Args:              cobra.ExactArgs(2),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid webhook ID: %w", err)
			}

			return be.DeleteWebhook(ctx, repo, id)
		},
	}

	return cmd
}

func webhookUpdateCommand() *cobra.Command {
	var events []string
	var secret string
	var active string
	var contentType string
	var url string
	var format string
	var bearerStdin bool
	var noBearer bool
	cmd := &cobra.Command{
		Use:               "update REPOSITORY WEBHOOK_ID",
		Short:             "Update a repository webhook",
		Args:              cobra.ExactArgs(2),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid webhook ID: %w", err)
			}

			wh, err := be.Webhook(ctx, repo, id)
			if err != nil {
				return err
			}

			newURL := wh.URL
			if url != "" {
				newURL = url
			}
			// Moving a webhook would send its token to the new URL, so
			// only those who may set a token may move it.
			if newURL != wh.URL && wh.BearerToken != "" && !isServerAdmin(ctx) {
				return proto.ErrUnauthorized
			}

			newSecret := wh.Secret
			if secret != "" {
				newSecret = secret
			}

			newActive := wh.Active
			if active != "" {
				active, err := strconv.ParseBool(active)
				if err != nil {
					return fmt.Errorf("invalid active value: %w", err)
				}

				newActive = active
			}

			newContentType := wh.ContentType
			if contentType != "" {
				var ct webhook.ContentType
				switch strings.ToLower(strings.TrimSpace(contentType)) {
				case "json":
					ct = webhook.ContentTypeJSON
				case "form":
					ct = webhook.ContentTypeForm
				default:
					return webhook.ErrInvalidContentType
				}
				newContentType = ct
			}

			newEvents := wh.Events
			if len(events) > 0 {
				var evs []webhook.Event
				for _, e := range events {
					ev, err := webhook.ParseEvent(e)
					if err != nil {
						return fmt.Errorf("invalid event: %w", err)
					}

					evs = append(evs, ev)
				}

				newEvents = evs
			}

			opts := backend.WebhookOptions{}
			if format != "" {
				if opts.Format, err = webhook.ParseFormat(format); err != nil {
					return err
				}
			}
			switch {
			case bearerStdin:
				token, err := readBearerToken(cmd)
				if err != nil {
					return err
				}
				opts.BearerToken = &token
			case noBearer:
				if !isServerAdmin(ctx) {
					return proto.ErrUnauthorized
				}
				opts.BearerToken = new(string)
			}

			return be.UpdateWebhook(ctx, repo, id, newURL, newContentType, newSecret, newEvents, newActive, opts)
		},
	}

	cmd.Flags().StringSliceVarP(&events, "events", "e", nil, fmt.Sprintf("events to trigger the webhook, available events are (%s)", strings.Join(webhookEvents, ", ")))
	cmd.Flags().StringVarP(&secret, "secret", "s", "", "secret to sign the webhook payload")
	cmd.Flags().StringVarP(&active, "active", "a", "", "whether the webhook is active")
	cmd.Flags().StringVarP(&contentType, "content-type", "c", "", "content type of the webhook payload, can be either `json` or `form`")
	cmd.Flags().StringVarP(&url, "url", "u", "", "webhook URL")
	cmd.Flags().StringVar(&format, "format", "", "payload format, can be either `icy` or `buildkite`")
	cmd.Flags().BoolVar(&bearerStdin, "bearer-stdin", false, "read a new bearer token from stdin (server admins only)")
	cmd.Flags().BoolVar(&noBearer, "no-bearer", false, "remove the bearer token (server admins only)")
	cmd.MarkFlagsMutuallyExclusive("bearer-stdin", "no-bearer")

	return cmd
}

// maxBearerInput caps how much of stdin is read for a bearer token.
const maxBearerInput = 8 << 10

// readBearerToken reads a bearer token from the command's stdin, so it
// never shows up in argv, shell history or logs. Only server admins may
// set one. One trailing newline is trimmed.
func readBearerToken(cmd *cobra.Command) (string, error) {
	if !isServerAdmin(cmd.Context()) {
		return "", proto.ErrUnauthorized
	}
	b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxBearerInput+1))
	if err != nil {
		return "", fmt.Errorf("read bearer token: %w", err)
	}
	if len(b) > maxBearerInput {
		return "", webhook.ErrInvalidBearerToken
	}
	token := strings.TrimSuffix(string(b), "\n")
	token = strings.TrimSuffix(token, "\r")
	if err := webhook.ValidateBearerToken(token); err != nil {
		return "", err //nolint:wrapcheck
	}
	return token, nil
}

// webhookAuth describes a webhook's authentication without showing it.
func webhookAuth(h webhook.Hook) string {
	if h.BearerToken != "" {
		return "bearer (set)"
	}
	return ""
}

func webhookInfoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "info REPOSITORY WEBHOOK_ID",
		Short:             "Show a repository webhook",
		Args:              cobra.ExactArgs(2),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid webhook ID: %w", err)
			}

			h, err := be.Webhook(ctx, repo, id)
			if err != nil {
				return err
			}

			events := make([]string, len(h.Events))
			for i, e := range h.Events {
				events[i] = e.String()
			}
			secret := ""
			if h.Secret != "" {
				secret = "(set)"
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "ID: %d\n", h.ID)                               //nolint:errcheck
			fmt.Fprintf(out, "URL: %s\n", utils.Sanitize(h.URL))             //nolint:errcheck
			fmt.Fprintf(out, "Events: %s\n", strings.Join(events, ","))      //nolint:errcheck
			fmt.Fprintf(out, "Format: %s\n", webhook.FormatOf(h.Format))     //nolint:errcheck
			fmt.Fprintf(out, "Content Type: %s\n", h.ContentType)            //nolint:errcheck
			fmt.Fprintf(out, "Secret: %s\n", secret)                         //nolint:errcheck
			fmt.Fprintf(out, "Auth: %s\n", webhookAuth(h))                   //nolint:errcheck
			fmt.Fprintf(out, "Active: %t\n", h.Active)                       //nolint:errcheck
			fmt.Fprintf(out, "Created At: %s\n", humanize.Time(h.CreatedAt)) //nolint:errcheck
			fmt.Fprintf(out, "Updated At: %s\n", humanize.Time(h.UpdatedAt)) //nolint:errcheck
			return nil
		},
	}

	return cmd
}

func webhookDeliveriesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deliveries",
		Short:   "Manage webhook deliveries",
		Aliases: []string{"delivery", "deliver"},
	}

	cmd.AddCommand(
		webhookDeliveriesListCommand(),
		webhookDeliveriesRedeliverCommand(),
		webhookDeliveriesGetCommand(),
	)

	return cmd
}

func webhookDeliveriesListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "list REPOSITORY WEBHOOK_ID",
		Short:             "List webhook deliveries",
		Args:              cobra.ExactArgs(2),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid webhook ID: %w", err)
			}

			dels, err := be.ListWebhookDeliveries(ctx, repo, id)
			if err != nil {
				return err
			}

			table := table.New().Headers("Status", "ID", "Event", "Created At")
			for _, d := range dels {
				status := "❌"
				if d.ResponseStatus >= 200 && d.ResponseStatus < 300 {
					status = "✅"
				}
				table = table.Row(
					status,
					d.ID.String(),
					d.Event.String(),
					humanize.Time(d.CreatedAt),
				)
			}

			// Deliveries still in the queue: not yet sent, or waiting
			// for a retry. A retry shares the ID of its first attempt.
			pending, err := be.ListPendingWebhookDeliveries(ctx, repo, id)
			if err != nil {
				return err
			}
			for _, p := range pending {
				table = table.Row(
					"⏳",
					p.ID.String(),
					p.Event.String(),
					humanize.Time(p.CreatedAt),
				)
			}
			cmd.Println(table)
			return nil
		},
	}

	return cmd
}

func webhookDeliveriesRedeliverCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "redeliver REPOSITORY WEBHOOK_ID DELIVERY_ID",
		Short:             "Redeliver a webhook delivery",
		Args:              cobra.ExactArgs(3),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid webhook ID: %w", err)
			}

			delID, err := uuid.Parse(args[2])
			if err != nil {
				return fmt.Errorf("invalid delivery ID: %w", err)
			}

			return be.RedeliverWebhookDelivery(ctx, repo, id, delID)
		},
	}

	return cmd
}

func webhookDeliveriesGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "get REPOSITORY WEBHOOK_ID DELIVERY_ID",
		Short:             "Get a webhook delivery",
		Args:              cobra.ExactArgs(3),
		PersistentPreRunE: requireRepo(access.AdminAccess),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			be := backend.FromContext(ctx)
			repo, _, err := repoFromContext(ctx)
			if err != nil {
				return err
			}

			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid webhook ID: %w", err)
			}

			delID, err := uuid.Parse(args[2])
			if err != nil {
				return fmt.Errorf("invalid delivery ID: %w", err)
			}

			del, err := be.WebhookDelivery(ctx, repo, id, delID)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "ID: %s\n", del.ID)                             //nolint:errcheck
			fmt.Fprintf(out, "Event: %s\n", del.Event)                       //nolint:errcheck
			fmt.Fprintf(out, "Request URL: %s\n", del.RequestURL)            //nolint:errcheck
			fmt.Fprintf(out, "Request Method: %s\n", del.RequestMethod)      //nolint:errcheck
			fmt.Fprintf(out, "Request Error: %s\n", del.RequestError.String) //nolint:errcheck
			fmt.Fprintf(out, "Request Headers:\n")                           //nolint:errcheck
			reqHeaders := strings.Split(del.RequestHeaders, "\n")
			for _, h := range reqHeaders {
				fmt.Fprintf(out, "  %s\n", h) //nolint:errcheck
			}

			fmt.Fprintf(out, "Request Body:\n") //nolint:errcheck
			reqBody := strings.Split(del.RequestBody, "\n")
			for _, b := range reqBody {
				fmt.Fprintf(out, "  %s\n", b) //nolint:errcheck
			}

			fmt.Fprintf(out, "Response Status: %d\n", del.ResponseStatus) //nolint:errcheck
			fmt.Fprintf(out, "Response Headers:\n")                       //nolint:errcheck
			resHeaders := strings.Split(del.ResponseHeaders, "\n")
			for _, h := range resHeaders {
				fmt.Fprintf(out, "  %s\n", h) //nolint:errcheck
			}

			fmt.Fprintf(out, "Response Body:\n") //nolint:errcheck
			resBody := strings.Split(del.ResponseBody, "\n")
			for _, b := range resBody {
				fmt.Fprintf(out, "  %s\n", b) //nolint:errcheck
			}

			return nil
		},
	}

	return cmd
}
