package mailer

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/resend/resend-go/v3"
)

type Resend struct {
	APIKey string
	From   string
}

func (r *Resend) SendVerification(ctx context.Context, to, code string) error {
	if r.APIKey == "" {
		return fmt.Errorf("Resend API key is not configured")
	}
	from, err := mail.ParseAddress(r.From)
	if err != nil {
		return fmt.Errorf("invalid Resend sender: %w", err)
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	client := resend.NewClient(r.APIKey)
	fromValue := strings.TrimSpace(r.From)
	if strings.ContainsAny(fromValue, "<>") {
		fromValue = from.String()
	} else {
		fromValue = from.Address
	}
	params := &resend.SendEmailRequest{
		From:    fromValue,
		To:      []string{recipient.Address},
		Subject: "Yunmatai verification code",
		Html: fmt.Sprintf(
			"<p>Your Yunmatai verification code is <strong>%s</strong>.</p><p>This code expires in 10 minutes. If you did not request it, you can ignore this email.</p>",
			code,
		),
	}
	if _, err = client.Emails.SendWithContext(ctx, params); err != nil {
		return fmt.Errorf("failed to send email via Resend: %w", err)
	}
	return nil
}
