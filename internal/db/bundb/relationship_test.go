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

package bundb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
)

type RelationshipTestSuite struct {
	BunDBStandardTestSuite
}

func (suite *RelationshipTestSuite) TestIsBlocked() {
	ctx := context.Background()

	account1 := suite.testAccounts["local_account_1"].ID
	account2 := suite.testAccounts["local_account_2"].ID

	// no blocks exist between account 1 and account 2
	blocked, err := suite.db.IsBlocked(ctx, account1, account2, false)
	suite.NoError(err)
	suite.False(blocked)

	blocked, err = suite.db.IsBlocked(ctx, account2, account1, false)
	suite.NoError(err)
	suite.False(blocked)

	// have account1 block account2
	suite.db.Put(ctx, &gtsmodel.Block{
		ID:              "01G202BCSXXJZ70BHB5KCAHH8C",
		URI:             "http://localhost:8080/some_block_uri_1",
		AccountID:       account1,
		TargetAccountID: account2,
	})

	// account 1 now blocks account 2
	blocked, err = suite.db.IsBlocked(ctx, account1, account2, false)
	suite.NoError(err)
	suite.True(blocked)

	// account 2 doesn't block account 1
	blocked, err = suite.db.IsBlocked(ctx, account2, account1, false)
	suite.NoError(err)
	suite.False(blocked)

	// a block exists in either direction between the two
	blocked, err = suite.db.IsBlocked(ctx, account1, account2, true)
	suite.NoError(err)
	suite.True(blocked)
	blocked, err = suite.db.IsBlocked(ctx, account2, account1, true)
	suite.NoError(err)
	suite.True(blocked)
}

func (suite *RelationshipTestSuite) TestGetBlock() {
	suite.Suite.T().Skip("TODO: implement")
}

func (suite *RelationshipTestSuite) TestGetRelationship() {
	suite.Suite.T().Skip("TODO: implement")
}

func (suite *RelationshipTestSuite) TestIsFollowing() {
	suite.Suite.T().Skip("TODO: implement")
}

func (suite *RelationshipTestSuite) TestIsMutualFollowing() {
	suite.Suite.T().Skip("TODO: implement")
}

func (suite *RelationshipTestSuite) AcceptFollowRequest() {
	for _, account := range suite.testAccounts {
		_, err := suite.db.AcceptFollowRequest(context.Background(), account.ID, "NON-EXISTENT-ID")
		if err != nil && !errors.Is(err, db.ErrNoEntries) {
			suite.Suite.Fail("error accepting follow request: %v", err)
		}
	}
}

func (suite *RelationshipTestSuite) GetAccountFollowRequests() {
	suite.Suite.T().Skip("TODO: implement")
}

func (suite *RelationshipTestSuite) GetAccountFollows() {
	suite.Suite.T().Skip("TODO: implement")
}

func (suite *RelationshipTestSuite) CountAccountFollows() {
	suite.Suite.T().Skip("TODO: implement")
}

func (suite *RelationshipTestSuite) GetAccountFollowedBy() {
	// TODO: more comprehensive tests here

	for _, account := range suite.testAccounts {
		var err error

		_, err = suite.db.GetAccountFollowedBy(context.Background(), account.ID, false)
		if err != nil {
			suite.Suite.Fail("error checking accounts followed by: %v", err)
		}

		_, err = suite.db.GetAccountFollowedBy(context.Background(), account.ID, true)
		if err != nil {
			suite.Suite.Fail("error checking localOnly accounts followed by: %v", err)
		}
	}
}

func (suite *RelationshipTestSuite) CountAccountFollowedBy() {
	suite.Suite.T().Skip("TODO: implement")
}

func TestRelationshipTestSuite(t *testing.T) {
	suite.Run(t, new(RelationshipTestSuite))
}
