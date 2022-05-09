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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/id"
)

func (suite *NotificationTestSuite) spamNotifs() {
	// spam a shit ton of notifs into the database
	// half of them will be for zork, the other half
	// will be for random accounts
	notifCount := 10000

	zork := suite.testAccounts["local_account_1"]

	for i := 0; i < notifCount; i++ {
		notifID, err := id.NewULID()
		if err != nil {
			panic(err)
		}

		var targetAccountID string
		if i%2 == 0 {
			targetAccountID = zork.ID
		} else {
			randomAssID, err := id.NewRandomULID()
			if err != nil {
				panic(err)
			}
			targetAccountID = randomAssID
		}

		statusID, err := id.NewRandomULID()
		if err != nil {
			panic(err)
		}

		originAccountID, err := id.NewRandomULID()
		if err != nil {
			panic(err)
		}

		notif := &gtsmodel.Notification{
			ID:               notifID,
			NotificationType: gtsmodel.NotificationFave,
			CreatedAt:        time.Now(),
			TargetAccountID:  targetAccountID,
			OriginAccountID:  originAccountID,
			StatusID:         statusID,
			Read:             false,
		}

		if err := suite.db.Put(context.Background(), notif); err != nil {
			panic(err)
		}
	}

	fmt.Printf("\n\n\nput %d notifs in the db\n\n\n", notifCount)
}

type NotificationTestSuite struct {
	BunDBStandardTestSuite
}

func (suite *NotificationTestSuite) TestGetNotificationsWithSpam() {
	suite.spamNotifs()
	testAccount := suite.testAccounts["local_account_1"]
	before := time.Now()
	notifications, err := suite.db.GetNotifications(context.Background(), testAccount.ID, 20, "ZZZZZZZZZZZZZZZZZZZZZZZZZZ", "00000000000000000000000000")
	suite.NoError(err)
	timeTaken := time.Since(before)
	fmt.Printf("\n\n\n withSpam: got %d notifications in %s\n\n\n", len(notifications), timeTaken)

	suite.NotNil(notifications)
	for _, n := range notifications {
		suite.Equal(testAccount.ID, n.TargetAccountID)
	}
}

func (suite *NotificationTestSuite) TestGetNotificationsWithoutSpam() {
	testAccount := suite.testAccounts["local_account_1"]
	before := time.Now()
	notifications, err := suite.db.GetNotifications(context.Background(), testAccount.ID, 20, "ZZZZZZZZZZZZZZZZZZZZZZZZZZ", "00000000000000000000000000")
	suite.NoError(err)
	timeTaken := time.Since(before)
	fmt.Printf("\n\n\n withoutSpam: got %d notifications in %s\n\n\n", len(notifications), timeTaken)

	suite.NotNil(notifications)
	for _, n := range notifications {
		suite.Equal(testAccount.ID, n.TargetAccountID)
		suite.NotNil(n.OriginAccount)
		suite.NotNil(n.TargetAccount)
		suite.NotNil(n.Status)
	}
}

func TestNotificationTestSuite(t *testing.T) {
	suite.Run(t, new(NotificationTestSuite))
}
