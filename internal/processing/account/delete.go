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

package account

import (
	"context"
	"errors"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/superseriousbusiness/gotosocial/internal/ap"
	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/messages"
	"golang.org/x/crypto/bcrypt"
)

// Delete handles the complete deletion of an account.
//
// To be done in this function:
// 1. Delete account's application(s), clients, and oauth tokens
// 2. Delete account's blocks
// 3. Delete account's emoji
// 4. Delete account's follow requests
// 5. Delete account's follows
// 6. Delete account's statuses
// 7. Delete account's media attachments
// 8. Delete account's mentions
// 9. Delete account's polls
// 10. Delete account's notifications
// 11. Delete account's bookmarks
// 12. Delete account's faves
// 13. Delete account's mutes
// 14. Delete account's streams
// 15. Delete account's tags
// 16. Delete account's user
// 17. Delete account's timeline
// 18. Delete account itself
func (p *processor) Delete(ctx context.Context, account *gtsmodel.Account, origin string) gtserror.WithCode {
	fields := logrus.Fields{
		"func":     "Delete",
		"username": account.Username,
	}
	if account.Domain != "" {
		fields["domain"] = account.Domain
	}
	l := logrus.WithFields(fields)

	l.Debug("beginning account delete process")

	// 1. Delete account's application(s), clients, and oauth tokens
	// we only need to do this step for local account since remote ones won't have any tokens or applications on our server
	if account.Domain == "" {
		// see if we can get a user for this account
		u := &gtsmodel.User{}
		if err := p.db.GetWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, u); err == nil {
			// we got one! select all tokens with the user's ID
			tokens := []*gtsmodel.Token{}
			if err := p.db.GetWhere(ctx, []db.Where{{Key: "user_id", Value: u.ID}}, &tokens); err == nil {
				// we have some tokens to delete
				for _, t := range tokens {
					// delete client(s) associated with this token
					if err := p.db.DeleteByID(ctx, t.ClientID, &gtsmodel.Client{}); err != nil {
						l.Errorf("error deleting oauth client: %s", err)
					}
					// delete application(s) associated with this token
					if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "client_id", Value: t.ClientID}}, &gtsmodel.Application{}); err != nil {
						l.Errorf("error deleting application: %s", err)
					}
					// delete the token itself
					if err := p.db.DeleteByID(ctx, t.ID, t); err != nil {
						l.Errorf("error deleting oauth token: %s", err)
					}
				}
			}
		}
	}

	// 2. Delete account's blocks
	l.Debug("deleting account blocks")
	// first delete any blocks that this account created
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, &[]*gtsmodel.Block{}); err != nil {
		l.Errorf("error deleting blocks created by account: %s", err)
	}

	// now delete any blocks that target this account
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "target_account_id", Value: account.ID}}, &[]*gtsmodel.Block{}); err != nil {
		l.Errorf("error deleting blocks targeting account: %s", err)
	}

	// 3. Delete account's emoji
	// nothing to do here

	// 4. Delete account's follow requests
	// TODO: federate these if necessary
	l.Debug("deleting account follow requests")
	// first delete any follow requests that this account created
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, &[]*gtsmodel.FollowRequest{}); err != nil {
		l.Errorf("error deleting follow requests created by account: %s", err)
	}

	// now delete any follow requests that target this account
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "target_account_id", Value: account.ID}}, &[]*gtsmodel.FollowRequest{}); err != nil {
		l.Errorf("error deleting follow requests targeting account: %s", err)
	}

	// 5. Delete account's follows
	// TODO: federate these if necessary
	l.Debug("deleting account follows")
	// first delete any follows that this account created
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, &[]*gtsmodel.Follow{}); err != nil {
		l.Errorf("error deleting follows created by account: %s", err)
	}

	// now delete any follows that target this account
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "target_account_id", Value: account.ID}}, &[]*gtsmodel.Follow{}); err != nil {
		l.Errorf("error deleting follows targeting account: %s", err)
	}

	// 6. Delete account's statuses
	l.Debug("deleting account statuses")
	// we'll select statuses 20 at a time so we don't wreck the db, and pass them through to the client api channel
	// Deleting the statuses in this way also handles 7. Delete account's media attachments, 8. Delete account's mentions, and 9. Delete account's polls,
	// since these are all attached to statuses.
	var maxID string
selectStatusesLoop:
	for {
		statuses, err := p.db.GetAccountStatuses(ctx, account.ID, 20, false, false, maxID, "", false, false, false)
		if err != nil {
			if err == db.ErrNoEntries {
				// no statuses left for this instance so we're done
				l.Infof("Delete: done iterating through statuses for account %s", account.Username)
				break selectStatusesLoop
			}
			// an actual error has occurred
			l.Errorf("Delete: db error selecting statuses for account %s: %s", account.Username, err)
			break selectStatusesLoop
		}

		for i, s := range statuses {
			// pass the status delete through the client api channel for processing
			s.Account = account
			l.Debug("putting status in the client api channel")
			p.clientWorker.Queue(messages.FromClientAPI{
				APObjectType:   ap.ObjectNote,
				APActivityType: ap.ActivityDelete,
				GTSModel:       s,
				OriginAccount:  account,
				TargetAccount:  account,
			})

			if err := p.db.DeleteByID(ctx, s.ID, s); err != nil {
				if err != db.ErrNoEntries {
					// actual error has occurred
					l.Errorf("Delete: db error status %s for account %s: %s", s.ID, account.Username, err)
					break selectStatusesLoop
				}
			}

			// if there are any boosts of this status, delete them as well
			boosts := []*gtsmodel.Status{}
			if err := p.db.GetWhere(ctx, []db.Where{{Key: "boost_of_id", Value: s.ID}}, &boosts); err != nil {
				if err != db.ErrNoEntries {
					// an actual error has occurred
					l.Errorf("Delete: db error selecting boosts of status %s for account %s: %s", s.ID, account.Username, err)
					break selectStatusesLoop
				}
			}

			for _, b := range boosts {
				if b.Account == nil {
					bAccount, err := p.db.GetAccountByID(ctx, b.AccountID)
					if err != nil {
						continue
					}
					b.Account = bAccount
				}

				l.Debug("putting boost undo in the client api channel")
				p.clientWorker.Queue(messages.FromClientAPI{
					APObjectType:   ap.ActivityAnnounce,
					APActivityType: ap.ActivityUndo,
					GTSModel:       s,
					OriginAccount:  b.Account,
					TargetAccount:  account,
				})

				if err := p.db.DeleteByID(ctx, b.ID, b); err != nil {
					if err != db.ErrNoEntries {
						// actual error has occurred
						l.Errorf("Delete: db error deleting boost with id %s: %s", b.ID, err)
						break selectStatusesLoop
					}
				}
			}

			// if this is the last status in the slice, set the maxID appropriately for the next query
			if i == len(statuses)-1 {
				maxID = s.ID
			}
		}
	}
	l.Debug("done deleting statuses")

	// 10. Delete account's notifications
	l.Debug("deleting account notifications")
	// first notifications created by account
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "origin_account_id", Value: account.ID}}, &[]*gtsmodel.Notification{}); err != nil {
		l.Errorf("error deleting notifications created by account: %s", err)
	}

	// now notifications targeting account
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "target_account_id", Value: account.ID}}, &[]*gtsmodel.Notification{}); err != nil {
		l.Errorf("error deleting notifications targeting account: %s", err)
	}

	// 11. Delete account's bookmarks
	l.Debug("deleting account bookmarks")
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, &[]*gtsmodel.StatusBookmark{}); err != nil {
		l.Errorf("error deleting bookmarks created by account: %s", err)
	}

	// 12. Delete account's faves
	// TODO: federate these if necessary
	l.Debug("deleting account faves")
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, &[]*gtsmodel.StatusFave{}); err != nil {
		l.Errorf("error deleting faves created by account: %s", err)
	}

	// 13. Delete account's mutes
	l.Debug("deleting account mutes")
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, &[]*gtsmodel.StatusMute{}); err != nil {
		l.Errorf("error deleting status mutes created by account: %s", err)
	}

	// 14. Delete account's streams
	// TODO

	// 15. Delete account's tags
	// TODO

	// 16. Delete account's user
	l.Debug("deleting account user")
	if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, &gtsmodel.User{}); err != nil {
		return gtserror.NewErrorInternalError(err)
	}

	// 17. Delete account's timeline
	// TODO

	// 18. Delete account itself
	// to prevent the account being created again, set all these fields and update it in the db
	// the account won't actually be *removed* from the database but it will be set to just a stub

	account.Note = ""
	account.DisplayName = ""
	account.AvatarMediaAttachmentID = ""
	account.AvatarRemoteURL = ""
	account.HeaderMediaAttachmentID = ""
	account.HeaderRemoteURL = ""
	account.Reason = ""
	account.Fields = []gtsmodel.Field{}
	account.HideCollections = true
	account.Discoverable = false

	account.SuspendedAt = time.Now()
	account.SuspensionOrigin = origin

	account, err := p.db.UpdateAccount(ctx, account)
	if err != nil {
		return gtserror.NewErrorInternalError(err)
	}

	l.Infof("deleted account with username %s from domain %s", account.Username, account.Domain)
	return nil
}

func (p *processor) DeleteLocal(ctx context.Context, account *gtsmodel.Account, form *apimodel.AccountDeleteRequest) gtserror.WithCode {
	fromClientAPIMessage := messages.FromClientAPI{
		APObjectType:   ap.ActorPerson,
		APActivityType: ap.ActivityDelete,
		TargetAccount:  account,
	}

	if form.DeleteOriginID == account.ID {
		// the account owner themself has requested deletion via the API, get their user from the db
		user := &gtsmodel.User{}
		if err := p.db.GetWhere(ctx, []db.Where{{Key: "account_id", Value: account.ID}}, user); err != nil {
			return gtserror.NewErrorInternalError(err)
		}

		// now check that the password they supplied is correct
		// make sure a password is actually set and bail if not
		if user.EncryptedPassword == "" {
			return gtserror.NewErrorForbidden(errors.New("user password was not set"))
		}

		// compare the provided password with the encrypted one from the db, bail if they don't match
		if err := bcrypt.CompareHashAndPassword([]byte(user.EncryptedPassword), []byte(form.Password)); err != nil {
			return gtserror.NewErrorForbidden(errors.New("invalid password"))
		}

		fromClientAPIMessage.OriginAccount = account
	} else {
		// the delete has been requested by some other account, grab it;
		// if we've reached this point we know it has permission already
		requestingAccount, err := p.db.GetAccountByID(ctx, form.DeleteOriginID)
		if err != nil {
			return gtserror.NewErrorInternalError(err)
		}

		fromClientAPIMessage.OriginAccount = requestingAccount
	}

	// put the delete in the processor queue to handle the rest of it asynchronously
	p.clientWorker.Queue(fromClientAPIMessage)

	return nil
}
