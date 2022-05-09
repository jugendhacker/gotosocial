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

package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/gruf/go-store/kv"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/suite"
	mediamodule "github.com/superseriousbusiness/gotosocial/internal/api/client/media"
	"github.com/superseriousbusiness/gotosocial/internal/api/model"
	"github.com/superseriousbusiness/gotosocial/internal/config"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/email"
	"github.com/superseriousbusiness/gotosocial/internal/federation"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/media"
	"github.com/superseriousbusiness/gotosocial/internal/messages"
	"github.com/superseriousbusiness/gotosocial/internal/oauth"
	"github.com/superseriousbusiness/gotosocial/internal/processing"
	"github.com/superseriousbusiness/gotosocial/internal/typeutils"
	"github.com/superseriousbusiness/gotosocial/internal/worker"
	"github.com/superseriousbusiness/gotosocial/testrig"
)

type MediaUpdateTestSuite struct {
	// standard suite interfaces
	suite.Suite
	db           db.DB
	storage      *kv.KVStore
	federator    federation.Federator
	tc           typeutils.TypeConverter
	mediaManager media.Manager
	oauthServer  oauth.Server
	emailSender  email.Sender
	processor    processing.Processor

	// standard suite models
	testTokens       map[string]*gtsmodel.Token
	testClients      map[string]*gtsmodel.Client
	testApplications map[string]*gtsmodel.Application
	testUsers        map[string]*gtsmodel.User
	testAccounts     map[string]*gtsmodel.Account
	testAttachments  map[string]*gtsmodel.MediaAttachment

	// item being tested
	mediaModule *mediamodule.Module
}

/*
	TEST INFRASTRUCTURE
*/

func (suite *MediaUpdateTestSuite) SetupSuite() {
	// setup standard items
	testrig.InitTestConfig()
	testrig.InitTestLog()

	fedWorker := worker.New[messages.FromFederator](-1, -1)
	clientWorker := worker.New[messages.FromClientAPI](-1, -1)

	suite.db = testrig.NewTestDB()
	suite.storage = testrig.NewTestStorage()
	suite.tc = testrig.NewTestTypeConverter(suite.db)
	suite.mediaManager = testrig.NewTestMediaManager(suite.db, suite.storage)
	suite.oauthServer = testrig.NewTestOauthServer(suite.db)
	suite.federator = testrig.NewTestFederator(suite.db, testrig.NewTestTransportController(testrig.NewMockHTTPClient(nil), suite.db, fedWorker), suite.storage, suite.mediaManager, fedWorker)
	suite.emailSender = testrig.NewEmailSender("../../../../web/template/", nil)
	suite.processor = testrig.NewTestProcessor(suite.db, suite.storage, suite.federator, suite.emailSender, suite.mediaManager, clientWorker, fedWorker)

	// setup module being tested
	suite.mediaModule = mediamodule.New(suite.processor).(*mediamodule.Module)
}

func (suite *MediaUpdateTestSuite) TearDownSuite() {
	if err := suite.db.Stop(context.Background()); err != nil {
		logrus.Panicf("error closing db connection: %s", err)
	}
}

func (suite *MediaUpdateTestSuite) SetupTest() {
	testrig.StandardDBSetup(suite.db, nil)
	testrig.StandardStorageSetup(suite.storage, "../../../../testrig/media")
	suite.testTokens = testrig.NewTestTokens()
	suite.testClients = testrig.NewTestClients()
	suite.testApplications = testrig.NewTestApplications()
	suite.testUsers = testrig.NewTestUsers()
	suite.testAccounts = testrig.NewTestAccounts()
	suite.testAttachments = testrig.NewTestAttachments()
}

func (suite *MediaUpdateTestSuite) TearDownTest() {
	testrig.StandardDBTeardown(suite.db)
	testrig.StandardStorageTeardown(suite.storage)
}

/*
	ACTUAL TESTS
*/

func (suite *MediaUpdateTestSuite) TestUpdateImage() {
	toUpdate := suite.testAttachments["local_account_1_unattached_1"]

	// set up the context for the request
	t := suite.testTokens["local_account_1"]
	oauthToken := oauth.DBTokenToToken(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(oauth.SessionAuthorizedApplication, suite.testApplications["application_1"])
	ctx.Set(oauth.SessionAuthorizedToken, oauthToken)
	ctx.Set(oauth.SessionAuthorizedUser, suite.testUsers["local_account_1"])
	ctx.Set(oauth.SessionAuthorizedAccount, suite.testAccounts["local_account_1"])

	// create the request
	buf, w, err := testrig.CreateMultipartFormData("", "", map[string]string{
		"id":          toUpdate.ID,
		"description": "new description!",
		"focus":       "-0.1,0.3",
	})
	if err != nil {
		panic(err)
	}
	ctx.Request = httptest.NewRequest(http.MethodPut, fmt.Sprintf("http://localhost:8080/%s/%s", mediamodule.BasePathV1, toUpdate.ID), bytes.NewReader(buf.Bytes())) // the endpoint we're hitting
	ctx.Request.Header.Set("Content-Type", w.FormDataContentType())
	ctx.Request.Header.Set("accept", "application/json")
	ctx.Params = gin.Params{
		gin.Param{
			Key:   mediamodule.IDKey,
			Value: toUpdate.ID,
		},
	}

	// do the actual request
	suite.mediaModule.MediaPUTHandler(ctx)

	// check response
	suite.EqualValues(http.StatusOK, recorder.Code)

	result := recorder.Result()
	defer result.Body.Close()
	b, err := ioutil.ReadAll(result.Body)
	suite.NoError(err)

	// reply should be an attachment
	attachmentReply := &model.Attachment{}
	err = json.Unmarshal(b, attachmentReply)
	suite.NoError(err)

	// the reply should contain the updated fields
	suite.Equal("new description!", attachmentReply.Description)
	suite.EqualValues("gif", attachmentReply.Type)
	suite.EqualValues(model.MediaMeta{
		Original: model.MediaDimensions{Width: 800, Height: 450, FrameRate: "", Duration: 0, Bitrate: 0, Size: "800x450", Aspect: 1.7777778},
		Small:    model.MediaDimensions{Width: 256, Height: 144, FrameRate: "", Duration: 0, Bitrate: 0, Size: "256x144", Aspect: 1.7777778},
		Focus:    model.MediaFocus{X: -0.1, Y: 0.3},
	}, attachmentReply.Meta)
	suite.Equal(toUpdate.Blurhash, attachmentReply.Blurhash)
	suite.Equal(toUpdate.ID, attachmentReply.ID)
	suite.Equal(toUpdate.URL, attachmentReply.URL)
	suite.NotEmpty(toUpdate.Thumbnail.URL, attachmentReply.PreviewURL)
}

func (suite *MediaUpdateTestSuite) TestUpdateImageShortDescription() {
	// set the min description length
	viper.Set(config.Keys.MediaDescriptionMinChars, 50)

	toUpdate := suite.testAttachments["local_account_1_unattached_1"]

	// set up the context for the request
	t := suite.testTokens["local_account_1"]
	oauthToken := oauth.DBTokenToToken(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(oauth.SessionAuthorizedApplication, suite.testApplications["application_1"])
	ctx.Set(oauth.SessionAuthorizedToken, oauthToken)
	ctx.Set(oauth.SessionAuthorizedUser, suite.testUsers["local_account_1"])
	ctx.Set(oauth.SessionAuthorizedAccount, suite.testAccounts["local_account_1"])

	// create the request
	buf, w, err := testrig.CreateMultipartFormData("", "", map[string]string{
		"id":          toUpdate.ID,
		"description": "new description!",
		"focus":       "-0.1,0.3",
	})
	if err != nil {
		panic(err)
	}
	ctx.Request = httptest.NewRequest(http.MethodPut, fmt.Sprintf("http://localhost:8080/%s/%s", mediamodule.BasePathV1, toUpdate.ID), bytes.NewReader(buf.Bytes())) // the endpoint we're hitting
	ctx.Request.Header.Set("Content-Type", w.FormDataContentType())
	ctx.Request.Header.Set("accept", "application/json")
	ctx.Params = gin.Params{
		gin.Param{
			Key:   mediamodule.IDKey,
			Value: toUpdate.ID,
		},
	}

	// do the actual request
	suite.mediaModule.MediaPUTHandler(ctx)

	// check response
	suite.EqualValues(http.StatusBadRequest, recorder.Code)

	result := recorder.Result()
	defer result.Body.Close()
	b, err := ioutil.ReadAll(result.Body)
	suite.NoError(err)

	// reply should be an error message
	suite.Equal(`{"error":"image description length must be between 50 and 500 characters (inclusive), but provided image description was 16 chars"}`, string(b))
}

func TestMediaUpdateTestSuite(t *testing.T) {
	suite.Run(t, new(MediaUpdateTestSuite))
}
