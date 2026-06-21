package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Sender interface {
	SendVerification(context.Context, string, string) error
}

type SMTP struct {
	Host, User, Password, From string
	Port                       int
}

func (s *SMTP) SendVerification(ctx context.Context, to, code string) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("invalid SMTP sender: %w", err)
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	address := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	dialer := &net.Dialer{Timeout: 12 * time.Second}
	var connection net.Conn
	if s.Port == 465 {
		connection, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	defer connection.Close()
	client, err := smtp.NewClient(connection, s.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if s.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server does not offer STARTTLS")
		}
		if err = client.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.User != "" {
		if err = client.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return err
	}
	if err = client.Rcpt(recipient.Address); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	subject := mime.QEncoding.Encode("UTF-8", "云码台注册验证码")
	body := strings.Join([]string{
		"From: " + s.From,
		"To: " + recipient.Address,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		"你的云码台注册验证码是：" + code,
		"",
		"验证码 10 分钟内有效。若非本人操作，请忽略此邮件。",
	}, "\r\n")
	if _, err = io.WriteString(w, body); err != nil {
		_ = w.Close()
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
