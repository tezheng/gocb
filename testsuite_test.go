package gocb

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	gojcbmock "github.com/couchbase/gocbcore/v9/jcbmock"
	"github.com/stretchr/testify/suite"
)

const (
	defaultServerVersion = "5.1.0"
)

var globalBucket *Bucket
var globalCollection *Collection
var globalCluster *testCluster

type IntegrationTestSuite struct {
	suite.Suite
}

func (suite *IntegrationTestSuite) SetupSuite() {
	var err error
	var connStr string
	var mock *gojcbmock.Mock
	var auth PasswordAuthenticator
	if globalConfig.Server == "" {
		if globalConfig.Version != "" {
			panic("version cannot be specified with mock")
		}

		mpath, err := gojcbmock.GetMockPath()
		if err != nil {
			panic(err.Error())
		}

		mock, err = gojcbmock.NewMock(mpath, 4, 1, 64, []gojcbmock.BucketSpec{
			{Name: "default", Type: gojcbmock.BCouchbase},
		}...)

		mock.Control(gojcbmock.NewCommand(gojcbmock.CSetCCCP,
			map[string]interface{}{"enabled": "true"}))
		mock.Control(gojcbmock.NewCommand(gojcbmock.CSetSASLMechanisms,
			map[string]interface{}{"mechs": []string{"SCRAM-SHA512"}}))

		if err != nil {
			panic(err.Error())
		}

		globalConfig.Version = mock.Version()

		var addrs []string
		for _, mcport := range mock.MemcachedPorts() {
			addrs = append(addrs, fmt.Sprintf("127.0.0.1:%d", mcport))
		}
		connStr = fmt.Sprintf("couchbase://%s", strings.Join(addrs, ","))
		auth = PasswordAuthenticator{
			Username: "default",
			Password: "",
		}
	} else {
		connStr = globalConfig.Server

		auth = PasswordAuthenticator{
			Username: globalConfig.User,
			Password: globalConfig.Password,
		}

		if globalConfig.Version == "" {
			globalConfig.Version = defaultServerVersion
		}
	}

	cluster, err := Connect(connStr, ClusterOptions{Authenticator: auth})
	if err != nil {
		panic(err.Error())
	}

	nodeVersion, err := newNodeVersion(globalConfig.Version, mock != nil)
	if err != nil {
		panic(err.Error())
	}

	globalCluster = &testCluster{
		Cluster:      cluster,
		Mock:         mock,
		Version:      nodeVersion,
		FeatureFlags: globalConfig.FeatureFlags,
	}

	globalBucket = globalCluster.Bucket(globalConfig.Bucket)

	err = globalBucket.WaitUntilReady(5*time.Second, &WaitUntilReadyOptions{DesiredState: ClusterStateOnline})
	if err != nil {
		panic(err.Error())
	}

	if globalConfig.Collection != "" {
		globalCollection = globalBucket.Collection(globalConfig.Collection)
	} else {
		globalCollection = globalBucket.DefaultCollection()
	}
}

func (suite *IntegrationTestSuite) TearDownSuite() {
	err := globalCluster.Close(nil)
	suite.Require().Nil(err, err)
}

func (suite *IntegrationTestSuite) createBreweryDataset(datasetName, service string) (int, error) {
	var dataset []testBreweryDocument
	err := loadJSONTestDataset(datasetName, &dataset)
	if err != nil {
		return 0, err
	}

	for i, doc := range dataset {
		doc.Service = service

		_, err := globalCollection.Upsert(fmt.Sprintf("%s%d", service, i), doc, nil)
		if err != nil {
			return 0, err
		}
	}

	return len(dataset), nil
}

func (suite *IntegrationTestSuite) tryUntil(deadline time.Time, interval time.Duration, fn func() bool) bool {
	for {
		success := fn()
		if success {
			return true
		}

		sleepDeadline := time.Now().Add(interval)
		if sleepDeadline.After(deadline) {
			return false
		}
		time.Sleep(sleepDeadline.Sub(time.Now()))
	}
}

func (suite *IntegrationTestSuite) skipIfUnsupported(code FeatureCode) {
	if globalCluster.NotSupportsFeature(code) {
		suite.T().Skipf("Skipping test because feature %s unsupported or disabled", code)
	}
}

type UnitTestSuite struct {
	suite.Suite
}

func TestIntegration(t *testing.T) {
	if testing.Short() {
		return
	}

	suite.Run(t, new(IntegrationTestSuite))
}

func TestUnit(t *testing.T) {
	suite.Run(t, new(UnitTestSuite))
}

func (suite *UnitTestSuite) defaultTimeoutConfig() TimeoutsConfig {
	return TimeoutsConfig{
		KVTimeout:         1000 * time.Second,
		KVDurableTimeout:  1000 * time.Second,
		AnalyticsTimeout:  1000 * time.Second,
		QueryTimeout:      1000 * time.Second,
		SearchTimeout:     1000 * time.Second,
		ManagementTimeout: 1000 * time.Second,
		ViewTimeout:       1000 * time.Second,
	}
}

func (suite *UnitTestSuite) bucket(name string, timeouts TimeoutsConfig, cli *mockClient) *Bucket {
	b := &Bucket{
		bucketName: name,

		timeoutsConfig: TimeoutsConfig{
			KVTimeout:         timeouts.KVTimeout,
			KVDurableTimeout:  timeouts.KVDurableTimeout,
			AnalyticsTimeout:  timeouts.AnalyticsTimeout,
			QueryTimeout:      timeouts.QueryTimeout,
			SearchTimeout:     timeouts.SearchTimeout,
			ManagementTimeout: timeouts.ManagementTimeout,
			ViewTimeout:       timeouts.ViewTimeout,
		},

		cachedClient: cli,
	}

	return b
}

func (suite *UnitTestSuite) newCluster() *Cluster {
	cluster := clusterFromOptions(ClusterOptions{
		Tracer: &noopTracer{},
	})

	return cluster
}

func (suite *UnitTestSuite) mustConvertToBytes(val interface{}) []byte {
	b, err := json.Marshal(val)
	suite.Require().Nil(err)

	return b
}
