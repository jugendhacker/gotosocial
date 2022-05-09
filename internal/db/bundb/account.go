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

package bundb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
	"github.com/superseriousbusiness/gotosocial/internal/cache"
	"github.com/superseriousbusiness/gotosocial/internal/config"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/uptrace/bun"
)

type accountDB struct {
	conn  *DBConn
	cache *cache.AccountCache
}

func (a *accountDB) newAccountQ(account *gtsmodel.Account) *bun.SelectQuery {
	return a.conn.
		NewSelect().
		Model(account).
		Relation("AvatarMediaAttachment").
		Relation("HeaderMediaAttachment")
}

func (a *accountDB) GetAccountByID(ctx context.Context, id string) (*gtsmodel.Account, db.Error) {
	return a.getAccount(
		ctx,
		func() (*gtsmodel.Account, bool) {
			return a.cache.GetByID(id)
		},
		func(account *gtsmodel.Account) error {
			return a.newAccountQ(account).Where("account.id = ?", id).Scan(ctx)
		},
	)
}

func (a *accountDB) GetAccountByURI(ctx context.Context, uri string) (*gtsmodel.Account, db.Error) {
	return a.getAccount(
		ctx,
		func() (*gtsmodel.Account, bool) {
			return a.cache.GetByURI(uri)
		},
		func(account *gtsmodel.Account) error {
			return a.newAccountQ(account).Where("account.uri = ?", uri).Scan(ctx)
		},
	)
}

func (a *accountDB) GetAccountByURL(ctx context.Context, url string) (*gtsmodel.Account, db.Error) {
	return a.getAccount(
		ctx,
		func() (*gtsmodel.Account, bool) {
			return a.cache.GetByURL(url)
		},
		func(account *gtsmodel.Account) error {
			return a.newAccountQ(account).Where("account.url = ?", url).Scan(ctx)
		},
	)
}

func (a *accountDB) getAccount(ctx context.Context, cacheGet func() (*gtsmodel.Account, bool), dbQuery func(*gtsmodel.Account) error) (*gtsmodel.Account, db.Error) {
	// Attempt to fetch cached account
	account, cached := cacheGet()

	if !cached {
		account = &gtsmodel.Account{}

		// Not cached! Perform database query
		err := dbQuery(account)
		if err != nil {
			return nil, a.conn.ProcessError(err)
		}

		// Place in the cache
		a.cache.Put(account)
	}

	return account, nil
}

func (a *accountDB) UpdateAccount(ctx context.Context, account *gtsmodel.Account) (*gtsmodel.Account, db.Error) {
	// Update the account's last-updated
	account.UpdatedAt = time.Now()

	// Update the account model in the DB
	_, err := a.conn.
		NewUpdate().
		Model(account).
		WherePK().
		Exec(ctx)
	if err != nil {
		return nil, a.conn.ProcessError(err)
	}

	// Place updated account in cache
	// (this will replace existing, i.e. invalidating)
	a.cache.Put(account)

	return account, nil
}

func (a *accountDB) GetInstanceAccount(ctx context.Context, domain string) (*gtsmodel.Account, db.Error) {
	account := new(gtsmodel.Account)

	q := a.newAccountQ(account)

	if domain != "" {
		q = q.
			Where("account.username = ?", domain).
			Where("account.domain = ?", domain)
	} else {
		host := viper.GetString(config.Keys.Host)
		q = q.
			Where("account.username = ?", host).
			WhereGroup(" AND ", whereEmptyOrNull("domain"))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, a.conn.ProcessError(err)
	}
	return account, nil
}

func (a *accountDB) GetAccountLastPosted(ctx context.Context, accountID string) (time.Time, db.Error) {
	status := new(gtsmodel.Status)

	q := a.conn.
		NewSelect().
		Model(status).
		Order("id DESC").
		Limit(1).
		Where("account_id = ?", accountID).
		Column("created_at")

	if err := q.Scan(ctx); err != nil {
		return time.Time{}, a.conn.ProcessError(err)
	}
	return status.CreatedAt, nil
}

func (a *accountDB) SetAccountHeaderOrAvatar(ctx context.Context, mediaAttachment *gtsmodel.MediaAttachment, accountID string) db.Error {
	if mediaAttachment.Avatar && mediaAttachment.Header {
		return errors.New("one media attachment cannot be both header and avatar")
	}

	var headerOrAVI string
	switch {
	case mediaAttachment.Avatar:
		headerOrAVI = "avatar"
	case mediaAttachment.Header:
		headerOrAVI = "header"
	default:
		return errors.New("given media attachment was neither a header nor an avatar")
	}

	// TODO: there are probably more side effects here that need to be handled
	if _, err := a.conn.
		NewInsert().
		Model(mediaAttachment).
		Exec(ctx); err != nil {
		return a.conn.ProcessError(err)
	}
	if _, err := a.conn.
		NewUpdate().
		Model(&gtsmodel.Account{}).
		Set(fmt.Sprintf("%s_media_attachment_id = ?", headerOrAVI), mediaAttachment.ID).
		Where("id = ?", accountID).
		Exec(ctx); err != nil {
		return a.conn.ProcessError(err)
	}

	return nil
}

func (a *accountDB) GetLocalAccountByUsername(ctx context.Context, username string) (*gtsmodel.Account, db.Error) {
	account := new(gtsmodel.Account)

	q := a.newAccountQ(account).
		Where("username = ?", strings.ToLower(username)). // usernames on our instance will always be lowercase
		WhereGroup(" AND ", whereEmptyOrNull("domain"))

	if err := q.Scan(ctx); err != nil {
		return nil, a.conn.ProcessError(err)
	}
	return account, nil
}

func (a *accountDB) GetAccountFaves(ctx context.Context, accountID string) ([]*gtsmodel.StatusFave, db.Error) {
	faves := new([]*gtsmodel.StatusFave)

	if err := a.conn.
		NewSelect().
		Model(faves).
		Where("account_id = ?", accountID).
		Scan(ctx); err != nil {
		return nil, a.conn.ProcessError(err)
	}

	return *faves, nil
}

func (a *accountDB) CountAccountStatuses(ctx context.Context, accountID string) (int, db.Error) {
	return a.conn.
		NewSelect().
		Model(&gtsmodel.Status{}).
		Where("account_id = ?", accountID).
		Count(ctx)
}

func (a *accountDB) GetAccountStatuses(ctx context.Context, accountID string, limit int, excludeReplies bool, excludeReblogs bool, maxID string, minID string, pinnedOnly bool, mediaOnly bool, publicOnly bool) ([]*gtsmodel.Status, db.Error) {
	statuses := []*gtsmodel.Status{}

	q := a.conn.
		NewSelect().
		Model(&statuses).
		Order("id DESC")

	if accountID != "" {
		q = q.Where("account_id = ?", accountID)
	}

	if limit != 0 {
		q = q.Limit(limit)
	}

	if excludeReplies {
		q = q.WhereGroup(" AND ", whereEmptyOrNull("in_reply_to_id"))
	}

	if excludeReblogs {
		q = q.WhereGroup(" AND ", whereEmptyOrNull("boost_of_id"))
	}

	if maxID != "" {
		q = q.Where("id < ?", maxID)
	}

	if minID != "" {
		q = q.Where("id > ?", minID)
	}

	if pinnedOnly {
		q = q.Where("pinned = ?", true)
	}

	if mediaOnly {
		// attachments are stored as a json object;
		// this implementation differs between sqlite and postgres,
		// so we have to be very thorough to cover all eventualities
		q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.
				Where("? IS NOT NULL", bun.Ident("attachments")).
				Where("? != ''", bun.Ident("attachments")).
				Where("? != 'null'", bun.Ident("attachments")).
				Where("? != '{}'", bun.Ident("attachments")).
				Where("? != '[]'", bun.Ident("attachments"))
		})
	}

	if publicOnly {
		q = q.Where("visibility = ?", gtsmodel.VisibilityPublic)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, a.conn.ProcessError(err)
	}

	if len(statuses) == 0 {
		return nil, db.ErrNoEntries
	}

	return statuses, nil
}

func (a *accountDB) GetAccountBlocks(ctx context.Context, accountID string, maxID string, sinceID string, limit int) ([]*gtsmodel.Account, string, string, db.Error) {
	blocks := []*gtsmodel.Block{}

	fq := a.conn.
		NewSelect().
		Model(&blocks).
		Where("block.account_id = ?", accountID).
		Relation("TargetAccount").
		Order("block.id DESC")

	if maxID != "" {
		fq = fq.Where("block.id < ?", maxID)
	}

	if sinceID != "" {
		fq = fq.Where("block.id > ?", sinceID)
	}

	if limit > 0 {
		fq = fq.Limit(limit)
	}

	if err := fq.Scan(ctx); err != nil {
		return nil, "", "", a.conn.ProcessError(err)
	}

	if len(blocks) == 0 {
		return nil, "", "", db.ErrNoEntries
	}

	accounts := []*gtsmodel.Account{}
	for _, b := range blocks {
		accounts = append(accounts, b.TargetAccount)
	}

	nextMaxID := blocks[len(blocks)-1].ID
	prevMinID := blocks[0].ID
	return accounts, nextMaxID, prevMinID, nil
}
