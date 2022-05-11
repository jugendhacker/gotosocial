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
	"fmt"
	"net/smtp"
	"text/template"

	"github.com/spf13/viper"
	"github.com/superseriousbusiness/gotosocial/internal/config"
)

// Sender contains functions for sending emails to instance users/new signups.
type Sender interface {
	// SendConfirmEmail sends a 'please confirm your email' style email to the given toAddress, with the given data.
	SendConfirmEmail(toAddress string, data ConfirmData) error

	// SendResetEmail sends a 'reset your password' style email to the given toAddress, with the given data.
	SendResetEmail(toAddress string, data ResetData) error
}

// NewSender returns a new email Sender interface with the given configuration, or an error if something goes wrong.
func NewSender() (Sender, error) {
	keys := config.Keys

	templateBaseDir := viper.GetString(keys.WebTemplateBaseDir)
	t, err := loadTemplates(templateBaseDir)
	if err != nil {
		return nil, err
	}

	username := viper.GetString(keys.SMTPUsername)
	password := viper.GetString(keys.SMTPPassword)
	host := viper.GetString(keys.SMTPHost)
	port := viper.GetInt(keys.SMTPPort)
	from := viper.GetString(keys.SMTPFrom)
	directTLS := viper.GetBool(keys.SMTPDirectTLS)

	return &sender{
		hostAddress:  fmt.Sprintf("%s:%d", host, port),
		serverName:   host,
		useDirectTLS: directTLS,
		from:         from,
		auth:         smtp.PlainAuth("", username, password, host),
		template:     t,
	}, nil
}

type sender struct {
	hostAddress  string
	useDirectTLS bool
	serverName   string
	from         string
	auth         smtp.Auth
	template     *template.Template
}
