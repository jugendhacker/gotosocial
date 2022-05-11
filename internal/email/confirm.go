/*
   GoToSocial
   Copyright (C) 2021-2022 GoToSocial Authors admin@gotosocial.org

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package email

import (
	"bytes"
	"crypto/tls"
	"net"
	"net/smtp"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/superseriousbusiness/gotosocial/internal/config"
)

const (
	confirmTemplate = "email_confirm_text.tmpl"
	confirmSubject  = "GoToSocial Email Confirmation"
)

func (s *sender) SendConfirmEmail(toAddress string, data ConfirmData) error {
	buf := &bytes.Buffer{}
	if err := s.template.ExecuteTemplate(buf, confirmTemplate, data); err != nil {
		return err
	}
	confirmBody := buf.String()

	msg, err := assembleMessage(confirmSubject, confirmBody, toAddress, s.from)
	if err != nil {
		return err
	}
	logrus.WithField("func", "SendConfirmEmail").Trace(s.hostAddress + "\n" + viper.GetString(config.Keys.SMTPUsername) + ":password" + "\n" + s.from + "\n" + toAddress + "\n\n" + string(msg) + "\n")
	if !s.useDirectTLS {
		return smtp.SendMail(s.hostAddress, s.auth, s.from, []string{toAddress}, msg)
	} else {
		conn, err := net.Dial("tcp", s.hostAddress)
		if err != nil {
			return err
		}
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.serverName})
		client, err := smtp.NewClient(tlsConn, s.hostAddress)
		if err != nil {
			return err
		}
		defer func() {
			client.Close()
			tlsConn.Close()
			conn.Close()
		}()
		if err = client.Auth(s.auth); err != nil {
			return err
		}
		if err = client.Mail(s.from); err != nil {
			return err
		}
		if err = client.Rcpt(toAddress); err != nil {
			return err
		}
		writer, err := client.Data()
		if err != nil {
			return err
		}
		_, err = writer.Write(msg)
		if err != nil {
			return err
		}
		err = writer.Close()
		if err != nil {
			return err
		}
		return client.Quit()
	}
}

// ConfirmData represents data passed into the confirm email address template.
type ConfirmData struct {
	// Username to be addressed.
	Username string
	// URL of the instance to present to the receiver.
	InstanceURL string
	// Name of the instance to present to the receiver.
	InstanceName string
	// Link to present to the receiver to click on and do the confirmation.
	// Should be a full link with protocol eg., https://example.org/confirm_email?token=some-long-token
	ConfirmLink string
}
