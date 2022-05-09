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

package user_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/suite"
	"github.com/superseriousbusiness/gotosocial/internal/api/client/user"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/oauth"
	"golang.org/x/crypto/bcrypt"
)

type PasswordChangeTestSuite struct {
	UserStandardTestSuite
}

func (suite *PasswordChangeTestSuite) TestPasswordChangePOST() {
	t := suite.testTokens["local_account_1"]
	oauthToken := oauth.DBTokenToToken(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(oauth.SessionAuthorizedApplication, suite.testApplications["application_1"])
	ctx.Set(oauth.SessionAuthorizedToken, oauthToken)
	ctx.Set(oauth.SessionAuthorizedUser, suite.testUsers["local_account_1"])
	ctx.Set(oauth.SessionAuthorizedAccount, suite.testAccounts["local_account_1"])
	ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:8080/%s", user.PasswordChangePath), nil)
	ctx.Request.Header.Set("accept", "application/json")
	ctx.Request.Form = url.Values{
		"old_password": {"password"},
		"new_password": {"peepeepoopoopassword"},
	}
	suite.userModule.PasswordChangePOSTHandler(ctx)

	// check response
	suite.EqualValues(http.StatusOK, recorder.Code)

	dbUser := &gtsmodel.User{}
	err := suite.db.GetByID(context.Background(), suite.testUsers["local_account_1"].ID, dbUser)
	suite.NoError(err)

	// new password should pass
	err = bcrypt.CompareHashAndPassword([]byte(dbUser.EncryptedPassword), []byte("peepeepoopoopassword"))
	suite.NoError(err)

	// old password should fail
	err = bcrypt.CompareHashAndPassword([]byte(dbUser.EncryptedPassword), []byte("password"))
	suite.EqualError(err, "crypto/bcrypt: hashedPassword is not the hash of the given password")
}

func (suite *PasswordChangeTestSuite) TestPasswordMissingOldPassword() {
	t := suite.testTokens["local_account_1"]
	oauthToken := oauth.DBTokenToToken(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(oauth.SessionAuthorizedApplication, suite.testApplications["application_1"])
	ctx.Set(oauth.SessionAuthorizedToken, oauthToken)
	ctx.Set(oauth.SessionAuthorizedUser, suite.testUsers["local_account_1"])
	ctx.Set(oauth.SessionAuthorizedAccount, suite.testAccounts["local_account_1"])
	ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:8080/%s", user.PasswordChangePath), nil)
	ctx.Request.Header.Set("accept", "application/json")
	ctx.Request.Form = url.Values{
		"new_password": {"peepeepoopoopassword"},
	}
	suite.userModule.PasswordChangePOSTHandler(ctx)

	// check response
	suite.EqualValues(http.StatusBadRequest, recorder.Code)

	result := recorder.Result()
	defer result.Body.Close()
	b, err := ioutil.ReadAll(result.Body)
	suite.NoError(err)
	suite.Equal(`{"error":"missing one or more required form values"}`, string(b))
}

func (suite *PasswordChangeTestSuite) TestPasswordIncorrectOldPassword() {
	t := suite.testTokens["local_account_1"]
	oauthToken := oauth.DBTokenToToken(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(oauth.SessionAuthorizedApplication, suite.testApplications["application_1"])
	ctx.Set(oauth.SessionAuthorizedToken, oauthToken)
	ctx.Set(oauth.SessionAuthorizedUser, suite.testUsers["local_account_1"])
	ctx.Set(oauth.SessionAuthorizedAccount, suite.testAccounts["local_account_1"])
	ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:8080/%s", user.PasswordChangePath), nil)
	ctx.Request.Header.Set("accept", "application/json")
	ctx.Request.Form = url.Values{
		"old_password": {"notright"},
		"new_password": {"peepeepoopoopassword"},
	}
	suite.userModule.PasswordChangePOSTHandler(ctx)

	// check response
	suite.EqualValues(http.StatusBadRequest, recorder.Code)

	result := recorder.Result()
	defer result.Body.Close()
	b, err := ioutil.ReadAll(result.Body)
	suite.NoError(err)
	suite.Equal(`{"error":"bad request: old password did not match"}`, string(b))
}

func (suite *PasswordChangeTestSuite) TestPasswordWeakNewPassword() {
	t := suite.testTokens["local_account_1"]
	oauthToken := oauth.DBTokenToToken(t)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(oauth.SessionAuthorizedApplication, suite.testApplications["application_1"])
	ctx.Set(oauth.SessionAuthorizedToken, oauthToken)
	ctx.Set(oauth.SessionAuthorizedUser, suite.testUsers["local_account_1"])
	ctx.Set(oauth.SessionAuthorizedAccount, suite.testAccounts["local_account_1"])
	ctx.Request = httptest.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:8080/%s", user.PasswordChangePath), nil)
	ctx.Request.Header.Set("accept", "application/json")
	ctx.Request.Form = url.Values{
		"old_password": {"password"},
		"new_password": {"peepeepoopoo"},
	}
	suite.userModule.PasswordChangePOSTHandler(ctx)

	// check response
	suite.EqualValues(http.StatusBadRequest, recorder.Code)

	result := recorder.Result()
	defer result.Body.Close()
	b, err := ioutil.ReadAll(result.Body)
	suite.NoError(err)
	suite.Equal(`{"error":"bad request: password is 94% strength, try including more special characters, using uppercase letters, using numbers or using a longer password"}`, string(b))
}

func TestPasswordChangeTestSuite(t *testing.T) {
	suite.Run(t, &PasswordChangeTestSuite{})
}
